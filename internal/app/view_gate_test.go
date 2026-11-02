package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/currency"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
)

// Тесты-гейты UI.
//
// Гейт проверяет, что go-gui не находит в кадре дефектов:
//
//   - TestFindings(gui.DebugAll) — дефекты идентичности: дублирующиеся
//     ID, фокусируемые и прокручиваемые виджеты без ID, расхождение
//     leaf/scope, «висящий» фокус, низкий контраст и т.п.;
//   - TestUnconsumedEvents() — обработчики, которые действуют по событи,
//     но не помечают его обработанным.
//
// Любая находка, кроме известных находок библиотеки, — падение теста.
//
// Три правила, без которых гейт нельзя написать:
//
//  1. Таймер в тестовом окне НЕ запускается: запущенный тикер каждую
//     секунду инвалидирует окно, а sweep ждёт «тишины» окна (settle).
//  2. Любой вызов, который теоретически может зависнуть, выполняется в
//     отдельной горутине под таймаутом.
//  3. Тесты не параллелятся: маска диагностики go-gui — общепроцессная.

// gateTimeout — предел ожидания для потенциально зависающих вызовов
// диагностики go-gui.
const gateTimeout = 15 * time.Second

// gateCleanuper — подмена *testing.T для gui.NewTestWindow: очистка окна
// выполняется под таймаутом.
//
// Зачем: если диагностика go-gui зависнет и удержит блокировку окна, то и
// обычная очистка (WindowCleanup) зависнет — тест не сможет упасть с
// внятным сообщением, а просто зависнет.
type gateCleanuper struct {
	t *testing.T
}

// Cleanup регистрирует функцию очистки теста.
func (c gateCleanuper) Cleanup(fn func()) {
	c.t.Cleanup(func() {
		done := make(chan struct{})

		go func() {
			defer close(done)
			fn()
		}()

		timer := time.NewTimer(gateTimeout)
		defer timer.Stop()

		select {
		case <-done:
		case <-timer.C:
			c.t.Logf("очистка окна не уложилась в %s "+
				"(вероятно, зависла диагностика go-gui)", gateTimeout)
		}
	})
}

// knownSweepFindings — находки sweep'а из кода самой библиотеки go-gui.
//
// Единственная такая находка — клик по внутренней строке поля ввода
// (gui.Input): библиотека вешает на неё OnClick (установка каретки), но
// событие не помечает обработанным, а родительское поле забирает фокус
// (go-gui v0.90.0, gui/view_input.go: inputOnClick). Это двойная
// обработка внутри одного виджета библиотеки — из приложения не лечится.
//
// Совпадение ищется по устойчивой части сообщения: координаты меняются от
// кадра к кадру, а идентификатор предка — нет.
var knownSweepFindings = []string{
	`"` + idDuration + `" above it would take focus on OnClick`,
}

// gateView — вид, который проверяет гейт.
type gateView struct {
	// name — имя вида в тексте падения.
	name string
	// prepare готовит состояние окна до первого кадра.
	prepare func(st *state.AppState)
	// view — генератор вида окна.
	view func(*gui.Window) gui.View
}

// gateViews — список проверяемых видов: корневой экран, три раздела
// диалога настроек и диалог журнала.
func gateViews() []gateView {
	return []gateView{
		{
			name: "root",
			view: rootView,
		},
		{
			name:    "settings/twitch",
			prepare: func(st *state.AppState) { st.SettingsState.CurrentSection = state.SettingSectionTwitch },
			view:    settingsDialogContent,
		},
		{
			name:    "settings/donation_alerts",
			prepare: func(st *state.AppState) { st.SettingsState.CurrentSection = state.SettingSectionDonationAlerts },
			view:    settingsDialogContent,
		},
		{
			name:    "settings/donatex",
			prepare: func(st *state.AppState) { st.SettingsState.CurrentSection = state.SettingSectionDonateX },
			view:    settingsDialogContent,
		},
		{
			name:    "settings/currencies",
			prepare: func(st *state.AppState) { st.SettingsState.CurrentSection = state.SettingSectionCurrencies },
			view:    settingsDialogContent,
		},
		{
			name:    "audit",
			prepare: func(st *state.AppState) { fillAuditLog(st) },
			view:    auditDialogView,
		},
	}
}

// fillAuditLog — несколько записей в журнале: диалог журнала — виртуальный
// список, и без записей в кадре нет ни списка, ни его строк.
func fillAuditLog(st *state.AppState) {
	st.Timer.Add(timer.SourceManual, time.Minute, "тест")
	st.Timer.SetRemaining(timer.SourceManual, 2*time.Minute)
	st.Timer.Add(timer.SourceTwitch, 30*time.Second, "подписка")
}

// newGateWindow — окно для гейт-тестов.
//
// Состояние окна собирается так же, как в приложении (onAppInit), но без
// запуска движка таймера и без фоновой подписки: гейту нужен только кадр.
// Таймер создаётся на минуту, чтобы в кадре был ненулевой остаток
// («01:00» на счётчике) — состояние детерминировано.
func newGateWindow(t *testing.T, v gateView) (*gui.Window, *state.AppState) {
	t.Helper()

	// Сеть в тестах не нужна: гейт-свип кликает кнопки, в том числе
	// «Обновить курсы». Подменяем загрузчик курсов — иначе тест ходил бы
	// в живой API.
	restore := fetchRates
	fetchRates = func(context.Context, string) (currency.Rates, error) {
		return currency.Rates{
			Base:      "RUB",
			Rates:     map[string]float64{"RUB": 1, "USD": 80, "EUR": 88},
			UpdatedAt: time.Now(),
		}, nil
	}
	t.Cleanup(func() { fetchRates = restore })

	st := state.InitialAppState()
	st.Timer = timer.New(time.Minute)

	// Начальные значения — как в onAppInit: вьюха читает остаток и
	// состояние из состояния окна, а не дёргает движок.
	st.RootState.Remaining = st.Timer.Remaining()
	st.RootState.TimerState = st.Timer.State()

	// Черновик настроек — как при открытии диалога настроек: диалог
	// редактирует глубокую копию, а не живые настройки.
	draft, err := st.Settings.Copy()
	if err != nil {
		t.Fatalf("копия настроек: %v", err)
	}
	st.SettingsState.Settings = draft

	if v.prepare != nil {
		v.prepare(st)
	}

	w := gui.NewTestWindow(gateCleanuper{t: t}, gui.WindowCfg{
		AppInfo: info,
		State:   st,
		Width:   800,
		Height:  600,
	})

	return w, st
}

// gateWithTimeout выполняет fn в отдельной горутине и ждёт результат не
// дольше limit. ok=false означает, что вызов не уложился — вызывающий
// обязан упасть с внятным сообщением, а не молча ждать.
func gateWithTimeout(limit time.Duration, fn func() []string) (found []string, ok bool) {
	done := make(chan []string, 1)

	go func() {
		done <- fn()
	}()

	timer := time.NewTimer(limit)
	defer timer.Stop()

	select {
	case found = <-done:
		return found, true
	case <-timer.C:
		return nil, false
	}
}

// assertNoFindings — общий хвост гейта: пустой список находок обязателен,
// любая находка печатается в тексте падения.
func assertNoFindings(t *testing.T, view string, found []string) {
	t.Helper()

	if len(found) == 0 {
		return
	}

	t.Fatalf("диагностика go-gui нашла дефекты в %s (%d):\n%s",
		view, len(found), strings.Join(found, "\n"))
}

// assertNoUnexpectedSweep — находки sweep'а: известные находки
// библиотеки допускаются (и печатаются в лог), остальные — падение.
func assertNoUnexpectedSweep(t *testing.T, view string, found []string) {
	t.Helper()

	var unexpected []string
	for _, f := range found {
		if isKnownSweepFinding(f) {
			t.Logf("известная находка go-gui в %s: %s", view, f)
			continue
		}
		unexpected = append(unexpected, f)
	}

	if len(unexpected) == 0 {
		return
	}

	t.Fatalf("sweep событий нашёл незаконченные обработчики в %s (%d):\n%s",
		view, len(unexpected), strings.Join(unexpected, "\n"))
}

// isKnownSweepFinding — находка из белого списка knownSweepFindings.
func isKnownSweepFinding(finding string) bool {
	for _, known := range knownSweepFindings {
		if strings.Contains(finding, known) {
			return true
		}
	}
	return false
}

// TestGateIdentityFindings — гейт дефектов идентичности на всех видах
// приложения.
func TestGateIdentityFindings(t *testing.T) {
	for _, v := range gateViews() {
		t.Run(v.name, func(t *testing.T) {
			w, _ := newGateWindow(t, v)

			w.TestRender(v.view)

			found, ok := gateWithTimeout(gateTimeout, func() []string {
				return w.TestFindings(gui.DebugAll)
			})
			if !ok {
				t.Fatalf("TestFindings на виде %q не уложился в %s "+
					"(зависание диагностики go-gui)", v.name, gateTimeout)
			}

			assertNoFindings(t, "виде "+v.name, found)
		})
	}
}

// TestGateUnconsumedEvents — гейт незаконченных обработчиков на всех видах
// приложения.
//
// Sweep проходит по дереву и нажимает каждую кнопку, поэтому окно для
// прогона — отдельное (своё на каждый вид): состояние после sweep не
// годится для следующей проверки.
//
// Известное ограничение: sweep вызывает обработчики приложения, поэтому
// он проверяет «сработало ли событие» только по косвенным признакам
// (пустой список находок).
func TestGateUnconsumedEvents(t *testing.T) {
	for _, v := range gateViews() {
		t.Run(v.name, func(t *testing.T) {
			w, _ := newGateWindow(t, v)

			w.TestRender(v.view)

			found, ok := gateWithTimeout(gateTimeout, func() []string {
				return w.TestUnconsumedEvents()
			})
			if !ok {
				t.Fatalf("sweep событий на виде %q не уложился в %s "+
					"(зависание sweep'а: чаще всего виноват запущенный "+
					"таймер — он инвалидирует окно каждую секунду)",
					v.name, gateTimeout)
			}

			assertNoUnexpectedSweep(t, "виде "+v.name, found)
		})
	}
}
