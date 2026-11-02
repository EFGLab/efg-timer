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

func donationAlertsProviderFor(st *state.AppState) (*providers.DonationAlertsProvider, bool) {
	provider := st.Providers.GetProvider(providers.ProviderNameDonationAlerts)
	daProvider, ok := provider.(*providers.DonationAlertsProvider)
	return daProvider, ok
}

func clearDonationAlertsSettings(cfg *settings.DonationAlertsConfig) {
	cfg.Enabled = false
	cfg.UserID = 0
	cfg.ExpiresAt = nil
}

func setDonationAlertsConnected(cfg *settings.DonationAlertsConfig, session providers.DonationAlertsSession) {
	cfg.Enabled = true
	cfg.UserID = session.UserID
	expiresAt := session.ExpiresAt
	cfg.ExpiresAt = &expiresAt
}

func copyDonationAlertsAuthFields(dst *settings.DonationAlertsConfig, src settings.DonationAlertsConfig) {
	dst.Enabled = src.Enabled
	dst.UserID = src.UserID
	dst.ExpiresAt = nil
	if src.ExpiresAt != nil {
		expiresAt := *src.ExpiresAt
		dst.ExpiresAt = &expiresAt
	}
}

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
		startDonationAlertsEventsOnce(w, p, st.Rules)
		w.InvalidateLayout()
	})
}

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

func restoreDonationAlertsSession(
	w *gui.Window,
	p *providers.DonationAlertsProvider,
	cfg settings.DonationAlertsConfig,
) {
	ctx := w.Ctx()

	if !cfg.Enabled {
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
			fmt.Println("donationalerts: open browser:", err)
		}
	}

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

	queueDonationAlertsConnected(w, ctx, p, session, "")
}

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
