// Package app — GUI-слой приложения: чистые функции отображения (формат MM:SS,
// лейблы кнопок, парсер длительности) и связка движка internal/timer с go-gui.
package app

import (
	"embed"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/EFGLab/efg-timer/internal/auditlog"
	"github.com/EFGLab/efg-timer/internal/helpers"
	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/appinfo"
	"github.com/go-gui-org/go-gui/gui/backend"
	"github.com/pkg/errors"
)

//go:embed assets/*
var assetsFS embed.FS

//go:embed appinfo.toml
var manifest []byte

var info = appinfo.MustParse(manifest)

// -X github.com/EFGLab/efg-timer/internal/app.Version=<v>.
var Version = "dev"

// counterFontSize — размер шрифта счётчика таймера: его задаёт тема (форк
// appTheme), а не вызывающий код.
const counterFontSize = 220

// appTheme — форк базовой темы: роль Display (счётчик таймера) становится
// крупной. Форк живёт здесь одной функцией, потому что его используют и
// приложение, и golden-тест.
func appTheme(base gui.Theme) gui.Theme {
	cfg := base.Cfg
	cfg.SizeTextXLarge = counterFontSize

	return gui.ThemeMaker(cfg)
}

func New() {
	appState := state.InitialAppState()

	gui.SetTheme(appTheme(gui.ThemeDark))

	rootWindow := gui.NewWindow(gui.WindowCfg{
		AppInfo: info,
		State:   appState,
		Width:   800,
		Height:  600,
		Title:   "EFG Timer",
		OnInit:  onAppInit,
	})

	backend.Run(rootWindow)
}

func onAppInit(w *gui.Window) {
	appState := gui.State[state.AppState](w)

	if err := gui.LoadSettings(w, &appState.Settings); err != nil {
		fmt.Println(errors.Wrap(err, "gui.LoadSettings"))
	}

	// Загрузка накладывает JSON поверх дефолтов, и таблица курсов из файла
	// перебила бы исправленные значения — чиним сломанную (миграция).
	appState.Settings.MigrateCurrencyRates()

	// Правила начисления — из загруженных настроек: до этого в сторе было
	// правило из дефолтов.
	applyRulesToStore(appState)

	appState.Timer = timer.New(appState.Settings.DefaultDuration.ToDuration())

	// Журнал операций живёт в файле: поднимаем его из файла и дописываем
	// новые записи.
	restoreAuditLog(appState.Timer)

	// Начальные значения: до первого тика вьюха уже должна показывать
	// верный остаток и состояние.
	appState.RootState.Remaining = appState.Timer.Remaining()
	appState.RootState.TimerState = appState.Timer.State()

	// Публикация тиков. gui.Stream — канонический путь go-gui для фонового
	// источника: значение применяется на frame thread (QueueCommand), после
	// чего окно инвалидируется. Отдельно InvalidateLayout не зовём — Stream
	// это делает сам. Отмена подписки — по контексту окна (w.Ctx()).
	appState.Timer.Subscribe(func(remaining time.Duration) {
		publishRemaining(remaining)
	})

	gui.Stream(w, tickChannel, func(w *gui.Window, remaining time.Duration) {
		// apply выполняется на frame thread — состояние окна здесь
		// мутировать можно.
		s := gui.State[state.AppState](w)
		s.RootState.Remaining = remaining
		// Состояние кнопки синхронизируем с движком: он сам останавливает
		// тикер, когда остаток дошёл до нуля, и об этом не уведомляет.
		s.RootState.TimerState = s.Timer.State()
	})

	twitchProvider := providers.NewTwitchProvider(providers.TwitchOptions{
		ClientID: settings.TwitchClientID,
		Store:    guiSecretStore{w: w},
	})

	// Провайдер DA: OAuth authorization code с client_secret (DA не
	// поддерживает PKCE). Секрет — переменная сборки, в настройках его нет.
	daProvider := providers.NewDonationAlertsProvider(providers.DonationAlertsOptions{
		ClientID:     settings.DonationAlertsClientID,
		ClientSecret: settings.DonationAlertsClientSecret,
		Store:        guiSecretStore{w: w},
	})
	appState.Providers = []providers.Provider{
		daProvider,
		twitchProvider,
	}

	go appState.Timer.Run(w.Ctx())

	// OBS-виджет: локальный HTTP-сервер со страницей /widget и SSE /events.
	startServer(w, appState)

	// Профилировщик — только по явному запросу: без EFG_TIMER_PPROF=1 порт
	// 6060 не поднимается.
	if pprofEnabled() {
		startPprof()
	}

	w.SetView(rootView)

	// Секреты читаются в горутине: OS-хранилище может показать запрос на
	// разблокировку. Настройки — снимок, чтобы не читать AppState из фона.
	twitchCfg := appState.Settings.Providers.Twitch
	go restoreTwitchSession(w, twitchProvider, twitchCfg)

	daCfg := appState.Settings.Providers.DonationAlerts
	go restoreDonationAlertsSession(w, daProvider, daCfg)
}

// auditRestoreLimit — сколько записей журнала поднимается из файла при
// старте (столько же, сколько таймер держит в памяти).
const auditRestoreLimit = 200

// restoreAuditLog поднимает журнал операций из файла и подписывает таймер на
// дописывание новых записей. Ошибка файла не роняет приложение: журнал
// остаётся только в памяти.
func restoreAuditLog(t *timer.Timer) {
	path, err := auditlog.Path(info.ID)
	if err != nil {
		fmt.Println(errors.Wrap(err, "auditlog.Path"))
		return
	}

	store := auditlog.NewStore(path)

	entries, err := store.Load(auditRestoreLimit)
	if err != nil {
		fmt.Println(errors.Wrap(err, "auditlog.Load"))
	} else {
		t.RestoreAudit(entries)
	}

	t.SubscribeAudit(func(e timer.AuditEntry) {
		if err := store.Append(e); err != nil {
			fmt.Println(errors.Wrap(err, "auditlog.Append"))
		}
	})
}

// tickChannel — канал публикаций значений таймера в окно. Окно в
// приложении одно, поэтому канал один на процесс: его читает gui.Stream
// в onAppInit, а пишут подписка на тики движка и обработчики кнопок
// Set/Reset. Через один канал порядок применения значений совпадает с
// порядком действий: отложенный тик не перезапишет значение, заданное
// кнопкой.
var tickChannel = make(chan time.Duration, 1)

// publishRemaining публикует остаток таймера в окно, не дожидаясь кадра.
// Отправка неблокирующая: горутина движка не должна вставать, если кадр не
// успевает. Устаревшее значение вытесняется свежим — показывать отставший
// остаток хуже, чем пропустить секунду.
func publishRemaining(remaining time.Duration) {
	select {
	case tickChannel <- remaining:
		return
	default:
	}

	select {
	case <-tickChannel:
	default:
	}

	select {
	case tickChannel <- remaining:
	default:
	}
}

// pprofEnv — переменная окружения, которой включается профилировщик.
const pprofEnv = "EFG_TIMER_PPROF"

// pprofAddr — адрес HTTP-сервера профилировщика.
const pprofAddr = "localhost:6060"

// pprofEnabled — включать ли профилировщик: только по явному запросу
// (EFG_TIMER_PPROF=1, принимаются также true/yes/on).
func pprofEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(pprofEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// startPprof поднимает профилировщик на localhost:6060. Обработчики
// pprof регистрируются в собственном mux: приложение DefaultServeMux не
// обслуживает. Ошибка запуска не прерывает работу.
func startPprof() {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	server := &http.Server{
		Addr:              pprofAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Println(errors.Wrap(err, "pprof: ListenAndServe"))
		}
	}()
}

// FormatMMSS рендерит остаток как MM:SS (минуты растут свободно: 3600s →
// 60:00). Отрицательные значения зажимаются в 00:00.
func FormatMMSS(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d / time.Second)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

// StateLabel возвращает лейбл главной кнопки Старт/Пауза для состояния таймера.
func StateLabel(s helpers.State) string {
	switch s {
	case helpers.Running:
		return "Pause"
	case helpers.Paused:
		return "Resume"
	default:
		return "Start"
	}
}

// ParseDurationMMSS разбирает содержимое поля длительности в формате «MM:SS».
// Строгий формат: минуты — целое неотрицательное, секунды — ровно два знака
// 0–59 (иначе «5:5» неоднозначен).
func ParseDurationMMSS(s string) (time.Duration, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("ожидается формат MM:SS")
	}
	min, errMin := strconv.Atoi(parts[0])
	sec, errSec := strconv.Atoi(parts[1])
	if errMin != nil || errSec != nil || len(parts[1]) != 2 || min < 0 || sec < 0 || sec > 59 {
		return 0, fmt.Errorf("некорректное значение MM:SS")
	}
	return time.Duration(min)*time.Minute + time.Duration(sec)*time.Second, nil
}

func dialogDismiss(ctx gui.EventCtx) {
	// Обработчик действует по событию — помечаем его обработанным.
	ctx.Consume()

	ctx.Window.DialogDismiss()
}
