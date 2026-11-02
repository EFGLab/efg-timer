// Неблокирующий вход DonationAlerts.
//
// Кнопка Enable отвечает сразу, вход идёт в горутине, результат и ошибка
// приходят в диалог через w.QueueCommand (frame thread). Токены в
// settings.json не попадают: сессию провайдер хранит в OS credential store.
// Подробности — references/donationalerts-auth.md.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
	"github.com/pkg/browser"
)

// donationAlertsProviderFor — провайдер DA из состояния окна.
func donationAlertsProviderFor(st *state.AppState) (*providers.DonationAlertsProvider, bool) {
	provider := st.Providers.GetProvider(providers.ProviderNameDonationAlerts)
	daProvider, ok := provider.(*providers.DonationAlertsProvider)
	return daProvider, ok
}

// clearDonationAlertsSettings — сброс метаданных входа; правила донатов
// (Time, PerAmount) не трогаем.
func clearDonationAlertsSettings(cfg *settings.DonationAlertsConfig) {
	cfg.Enabled = false
	cfg.UserID = 0
	cfg.ExpiresAt = nil
}

// setDonationAlertsConnected — метаданные успешного входа.
func setDonationAlertsConnected(cfg *settings.DonationAlertsConfig, session providers.DonationAlertsSession) {
	cfg.Enabled = true
	cfg.UserID = session.UserID
	expiresAt := session.ExpiresAt
	cfg.ExpiresAt = &expiresAt
}

// copyDonationAlertsAuthFields переносит только состояние входа.
func copyDonationAlertsAuthFields(dst *settings.DonationAlertsConfig, src settings.DonationAlertsConfig) {
	dst.Enabled = src.Enabled
	dst.UserID = src.UserID
	dst.ExpiresAt = nil
	if src.ExpiresAt != nil {
		expiresAt := *src.ExpiresAt
		dst.ExpiresAt = &expiresAt
	}
}

// postDonationAlertsAuthError показывает ошибку входа в диалоге.
func postDonationAlertsAuthError(w *gui.Window, err error) {
	fmt.Println("donationalerts:", err)
	w.QueueCommand(func(w *gui.Window) {
		st := gui.State[state.AppState](w)
		st.SettingsState.DonationAlertsAuth.Pending = false
		st.SettingsState.DonationAlertsAuth.Error = err.Error()
		st.SettingsState.DonationAlertsAuth.Cancel = nil
		w.InvalidateLayout()
	})
}

// queueDonationAlertsConnected фиксирует вход в настройках и состоянии
// диалога, сохраняет настройки и запускает поток событий.
//
// Вызывается через w.QueueCommand — это frame thread: только здесь можно
// мутировать состояние окна.
func queueDonationAlertsConnected(
	w *gui.Window,
	ctx context.Context,
	p *providers.DonationAlertsProvider,
	session providers.DonationAlertsSession,
	errText string,
) {
	w.QueueCommand(func(w *gui.Window) {
		if ctx.Err() != nil {
			return
		}
		st := gui.State[state.AppState](w)
		setDonationAlertsConnected(&st.Settings.Providers.DonationAlerts, session)
		copyDonationAlertsAuthFields(&st.SettingsState.Settings.Providers.DonationAlerts, st.Settings.Providers.DonationAlerts)
		st.SettingsState.DonationAlertsAuth = state.DonationAlertsAuthState{Error: errText}
		if err := gui.SaveSettings(w, st.Settings); err != nil {
			fmt.Println("donationalerts: save settings:", err)
		}
		// Поток событий запускаем здесь: он берёт таймер из состояния окна.
		startDonationAlertsEventsOnce(w, p, st.Rules)
		w.InvalidateLayout()
	})
}

// startDonationAlertsEventsOnce запускает поток событий DA, если он ещё не
// идёт: у провайдера поток один.
func startDonationAlertsEventsOnce(
	w *gui.Window,
	p *providers.DonationAlertsProvider,
	store *rules.Store,
) {
	if p.EventsRunning() {
		return
	}
	startDonationAlertsEvents(w, p, store)
}

// restoreDonationAlertsSession читает секрет при старте приложения.
//
// Секреты читаются только из горутины (OS-хранилище может показать запрос
// на разблокировку), в UI возвращаемся через w.QueueCommand.
//
// Провайдер выключен — секрет удаляется; включён, но сессии нет —
// настройки сбрасываются; сессия есть — метаданные восстанавливаются и
// запускается поток событий.
func restoreDonationAlertsSession(
	w *gui.Window,
	p *providers.DonationAlertsProvider,
	cfg settings.DonationAlertsConfig,
) {
	ctx := w.Ctx()

	if !cfg.Enabled {
		// Секрет остался после выключения — не держим его без провайдера.
		if _, found, err := p.LoadSession(ctx); err == nil && found {
			_ = p.DeleteSession(ctx)
		}
		return
	}
	session, found, err := p.LoadSession(ctx)
	if err != nil {
		postDonationAlertsAuthError(w, fmt.Errorf("не удалось прочитать сессию DonationAlerts: %w", err))
		return
	}
	if !found {
		w.QueueCommand(func(w *gui.Window) {
			st := gui.State[state.AppState](w)
			clearDonationAlertsSettings(&st.Settings.Providers.DonationAlerts)
			copyDonationAlertsAuthFields(&st.SettingsState.Settings.Providers.DonationAlerts, st.Settings.Providers.DonationAlerts)
			if err := gui.SaveSettings(w, st.Settings); err != nil {
				fmt.Println("donationalerts: save settings:", err)
			}
			w.InvalidateLayout()
		})
		return
	}

	// Сессия есть — возвращаем метаданные в настройки и запускаем поток
	// событий (поток стартует на frame thread).
	w.QueueCommand(func(w *gui.Window) {
		st := gui.State[state.AppState](w)
		setDonationAlertsConnected(&st.Settings.Providers.DonationAlerts, session)
		copyDonationAlertsAuthFields(&st.SettingsState.Settings.Providers.DonationAlerts, st.Settings.Providers.DonationAlerts)
		if err := gui.SaveSettings(w, st.Settings); err != nil {
			fmt.Println("donationalerts: save settings:", err)
		}
		startDonationAlertsEventsOnce(w, p, st.Rules)
		w.InvalidateLayout()
	})
}

// viewDialogSettingsDonationAlertsEnableHandler — кнопка Enable: отвечает
// сразу, вход идёт в горутине; событие помечаем обработанным (go-gui).
func viewDialogSettingsDonationAlertsEnableHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	p, ok := donationAlertsProviderFor(st)
	if !ok || p.IsRunning() || st.SettingsState.DonationAlertsAuth.Pending {
		return
	}

	authCtx, cancel := context.WithCancel(ctx.Window.Ctx())
	st.SettingsState.DonationAlertsAuth = state.DonationAlertsAuthState{Pending: true, Cancel: cancel}
	ctx.Window.InvalidateLayout()
	go runDonationAlertsAuth(ctx.Window, p, authCtx, cancel)
	ctx.Consume()
}

// runDonationAlertsAuth — вход DA целиком (из горутины).
//
// BeginAuth поднимает loopback-сервер и отдаёт ссылку для браузера;
// браузер открываем сами. WaitAuth обменивает код на токен и сохраняет
// сессию в OS-хранилище.
func runDonationAlertsAuth(w *gui.Window, p *providers.DonationAlertsProvider, ctx context.Context, cancel context.CancelFunc) {
	defer cancel()

	auth, err := p.BeginAuth(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if ctx.Err() == nil {
			postDonationAlertsAuthError(w, fmt.Errorf("не удалось начать вход DonationAlerts: %w", err))
		}
		return
	}

	// Ссылка — в диалоге: если браузер не открылся, владелец жмёт кнопку сам.
	w.QueueCommand(func(w *gui.Window) {
		if ctx.Err() != nil {
			return
		}
		st := gui.State[state.AppState](w)
		st.SettingsState.DonationAlertsAuth.Pending = true
		st.SettingsState.DonationAlertsAuth.AuthorizeURL = auth.AuthorizeURL
		st.SettingsState.DonationAlertsAuth.Error = ""
		w.InvalidateLayout()
	})

	if ctx.Err() == nil {
		if err := browser.OpenURL(auth.AuthorizeURL); err != nil {
			// Не фатально: ссылка остаётся в диалоге.
			fmt.Println("donationalerts: open browser:", err)
		}
	}

	// WaitAuth зовём всегда, даже если контекст уже отменён: в нём же
	// снимается состояние входа у провайдера — иначе следующий Enable
	// упрётся в «вход уже идёт».
	session, err := p.WaitAuth(ctx, auth)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if ctx.Err() == nil {
			postDonationAlertsAuthError(w, fmt.Errorf("не удалось войти в DonationAlerts: %w", err))
		}
		return
	}
	if ctx.Err() != nil {
		return
	}

	// Сессия уже сохранена провайдером (WaitAuth → SaveSession).
	queueDonationAlertsConnected(w, ctx, p, session, "")
}

// viewDialogSettingsDonationAlertsCancelHandler — кнопка Cancel во время
// входа: закрываем loopback-сервер и сбрасываем состояние диалога.
func viewDialogSettingsDonationAlertsCancelHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	cancel := st.SettingsState.DonationAlertsAuth.Cancel
	st.SettingsState.DonationAlertsAuth = state.DonationAlertsAuthState{}
	if p, ok := donationAlertsProviderFor(st); ok {
		p.CancelAuth()
	}
	if cancel != nil {
		cancel()
	}
	ctx.Window.InvalidateLayout()
	ctx.Consume()
}

// viewDialogSettingsDonationAlertsDisableHandler — кнопка Disable:
// сбрасываем настройки, останавливаем поток событий и удаляем секрет.
//
// Снимок сессии и эпохи снимаем до отмены входа: выход делается в
// горутине, а эпоха нужна, чтобы новый вход не был стёрт старым выходом.
func viewDialogSettingsDonationAlertsDisableHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	p, ok := donationAlertsProviderFor(st)
	cancelAuth := st.SettingsState.DonationAlertsAuth.Cancel
	var (
		oldSession providers.DonationAlertsSession
		hadSession bool
		epoch      uint64
	)
	if ok {
		oldSession, hadSession = p.Session()
		epoch = p.SessionEpoch()
		p.CancelAuth()
		p.StopEvents()
		p.Disable()
	}
	if cancelAuth != nil {
		cancelAuth()
	}

	clearDonationAlertsSettings(&st.Settings.Providers.DonationAlerts)
	copyDonationAlertsAuthFields(&st.SettingsState.Settings.Providers.DonationAlerts, st.Settings.Providers.DonationAlerts)
	st.SettingsState.DonationAlertsAuth = state.DonationAlertsAuthState{}
	if err := gui.SaveSettings(ctx.Window, st.Settings); err != nil {
		fmt.Println("donationalerts: save settings:", err)
	}
	ctx.Window.InvalidateLayout()
	ctx.Consume()

	if !ok {
		return
	}
	go func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := p.LogoutAt(cleanupCtx, oldSession, hadSession, epoch); err != nil {
			fmt.Println("donationalerts: logout:", err)
		}
	}()
}

// viewDialogSettingsDonationAlertsOpenHandler — кнопка «Открыть
// DonationAlerts»: открывает сохранённую ссылку входа в браузере.
func viewDialogSettingsDonationAlertsOpenHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	uri := st.SettingsState.DonationAlertsAuth.AuthorizeURL
	if uri != "" {
		if err := browser.OpenURL(uri); err != nil {
			fmt.Println("donationalerts: open browser:", err)
		}
	}
	ctx.Consume()
}
