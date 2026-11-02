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

func twitchProviderFor(st *state.AppState) (*providers.TwitchProvider, bool) {
	provider := st.Providers.GetProvider(providers.ProviderNameTwitch)
	twitchProvider, ok := provider.(*providers.TwitchProvider)
	return twitchProvider, ok
}

func clearTwitchSettings(cfg *settings.TwitchConfig) {
	cfg.Enabled = false
	cfg.Login = ""
	cfg.UserID = ""
	cfg.ExpiresAt = nil
}

func setTwitchConnected(cfg *settings.TwitchConfig, user providers.TwitchUser, session providers.TwitchSession) {
	cfg.Enabled = true
	cfg.Login = user.Login
	cfg.UserID = user.ID
	expiresAt := session.ExpiresAt
	cfg.ExpiresAt = &expiresAt
}

func copyTwitchAuthFields(dst *settings.TwitchConfig, src settings.TwitchConfig) {
	dst.Enabled = src.Enabled
	dst.Login = src.Login
	dst.UserID = src.UserID
	dst.ExpiresAt = nil
	if src.ExpiresAt != nil {
		expiresAt := *src.ExpiresAt
		dst.ExpiresAt = &expiresAt
	}
}

func postTwitchAuthError(w *gui.Window, err error) {
	w.QueueCommand(func(w *gui.Window) {
		st := gui.State[state.AppState](w)
		st.SettingsState.TwitchAuth.Pending = false
		st.SettingsState.TwitchAuth.Error = err.Error()
		st.SettingsState.TwitchAuth.Cancel = nil
		w.InvalidateLayout()
	})
}

func queueTwitchConnected(w *gui.Window, ctx context.Context, p *providers.TwitchProvider, user providers.TwitchUser, session providers.TwitchSession, errText string) {
	w.QueueCommand(func(w *gui.Window) {
		if ctx.Err() != nil {
			return
		}
		st := gui.State[state.AppState](w)
		setTwitchConnected(&st.Settings.Providers.Twitch, user, session)
		copyTwitchAuthFields(&st.SettingsState.Settings.Providers.Twitch, st.Settings.Providers.Twitch)
		st.SettingsState.TwitchAuth = state.TwitchAuthState{Error: errText}
		if err := gui.SaveSettings(w, st.Settings); err != nil {
			fmt.Println("twitch: save settings:", err)
		}
		startTwitchEventsOnce(w, p, st.Rules)
		w.InvalidateLayout()
	})
}

func startTwitchEventsOnce(w *gui.Window, p *providers.TwitchProvider, store *rules.Store) {
	if p.EventsRunning() {
		return
	}
	startTwitchEvents(w, p, store)
}

func restoreTwitchSession(w *gui.Window, p *providers.TwitchProvider, cfg settings.TwitchConfig) {
	ctx := w.Ctx()

	if !cfg.Enabled {
		if _, found, err := p.LoadSession(ctx); err == nil && found {
			_ = p.DeleteSession(ctx)
		}
		return
	}

	session, found, err := p.LoadSession(ctx)
	if err != nil {
		postTwitchAuthError(w, fmt.Errorf("не удалось прочитать сессию Twitch: %w", err))
		return
	}
	if !found {
		w.QueueCommand(func(w *gui.Window) {
			st := gui.State[state.AppState](w)
			clearTwitchSettings(&st.Settings.Providers.Twitch)
			copyTwitchAuthFields(&st.SettingsState.Settings.Providers.Twitch, st.Settings.Providers.Twitch)
			if err := gui.SaveSettings(w, st.Settings); err != nil {
				fmt.Println("twitch: save settings:", err)
			}
			w.InvalidateLayout()
		})
		return
	}

	if cfg.Login == "" || session.ExpiresAt.IsZero() || !time.Now().Before(session.ExpiresAt) {
		user, updated, err := p.FetchUser(ctx, session)
		if err != nil {
			postTwitchAuthError(w, fmt.Errorf("не удалось проверить сессию Twitch: %w", err))
			return
		}

		w.QueueCommand(func(w *gui.Window) {
			st := gui.State[state.AppState](w)
			setTwitchConnected(&st.Settings.Providers.Twitch, user, updated)
			copyTwitchAuthFields(&st.SettingsState.Settings.Providers.Twitch, st.Settings.Providers.Twitch)
			if err := gui.SaveSettings(w, st.Settings); err != nil {
				fmt.Println("twitch: save settings:", err)
			}
			startTwitchEventsOnce(w, p, st.Rules)
			w.InvalidateLayout()
		})
		return
	}

	w.QueueCommand(func(w *gui.Window) {
		st := gui.State[state.AppState](w)
		copyTwitchAuthFields(&st.SettingsState.Settings.Providers.Twitch, cfg)
		startTwitchEventsOnce(w, p, st.Rules)
		w.InvalidateLayout()
	})
}

func viewDialogSettingsTwitchEnableHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	p, ok := twitchProviderFor(st)
	if !ok || p.IsRunning() || st.SettingsState.TwitchAuth.Pending {
		return
	}

	authCtx, cancel := context.WithCancel(ctx.Window.Ctx())
	st.SettingsState.TwitchAuth = state.TwitchAuthState{Pending: true, Cancel: cancel}
	ctx.Window.InvalidateLayout()
	go runTwitchAuth(ctx.Window, p, authCtx, cancel)
	ctx.Consume()
}

func runTwitchAuth(w *gui.Window, p *providers.TwitchProvider, ctx context.Context, cancel context.CancelFunc) {
	defer cancel()

	auth, err := p.BeginDeviceAuth(ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if ctx.Err() == nil {
			postTwitchAuthError(w, fmt.Errorf("не удалось начать вход Twitch: %w", err))
		}
		return
	}
	if ctx.Err() != nil {
		return
	}

	w.QueueCommand(func(w *gui.Window) {
		if ctx.Err() != nil {
			return
		}
		st := gui.State[state.AppState](w)
		st.SettingsState.TwitchAuth.Pending = true
		st.SettingsState.TwitchAuth.UserCode = auth.UserCode
		st.SettingsState.TwitchAuth.VerificationURI = auth.VerificationURI
		st.SettingsState.TwitchAuth.Error = ""
		w.InvalidateLayout()
	})

	if err := browser.OpenURL(auth.VerificationURI); err != nil {
		fmt.Println("twitch: open browser:", err)
	}

	session, err := p.PollDeviceAuth(ctx, auth)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		if ctx.Err() == nil {
			postTwitchAuthError(w, fmt.Errorf("не удалось войти в Twitch: %w", err))
		}
		return
	}
	if ctx.Err() != nil {
		return
	}

	if err := p.SaveSession(ctx, session); err != nil {
		if ctx.Err() == nil {
			postTwitchAuthError(w, fmt.Errorf("не удалось сохранить сессию Twitch: %w", err))
		}
		return
	}
	if ctx.Err() != nil {
		return
	}

	user, updated, err := p.FetchUser(ctx, session)
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			return
		}
		fmt.Println("twitch: fetch user:", err)
		queueTwitchConnected(w, ctx, p, providers.TwitchUser{}, updated, "не удалось получить канал Twitch: "+err.Error())
		return
	}
	if ctx.Err() != nil {
		return
	}
	queueTwitchConnected(w, ctx, p, user, updated, "")
}

func viewDialogSettingsTwitchCancelHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	cancel := st.SettingsState.TwitchAuth.Cancel
	st.SettingsState.TwitchAuth = state.TwitchAuthState{}
	if p, ok := twitchProviderFor(st); ok {
		p.CancelAuth()
	}
	if cancel != nil {
		cancel()
	}
	ctx.Window.InvalidateLayout()
	ctx.Consume()
}

func viewDialogSettingsTwitchDisableHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	p, ok := twitchProviderFor(st)
	cancelAuth := st.SettingsState.TwitchAuth.Cancel
	var (
		oldSession providers.TwitchSession
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

	clearTwitchSettings(&st.Settings.Providers.Twitch)
	copyTwitchAuthFields(&st.SettingsState.Settings.Providers.Twitch, st.Settings.Providers.Twitch)
	st.SettingsState.TwitchAuth = state.TwitchAuthState{}
	if err := gui.SaveSettings(ctx.Window, st.Settings); err != nil {
		fmt.Println("twitch: save settings:", err)
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
			fmt.Println("twitch: logout:", err)
		}
	}()
}

func viewDialogSettingsTwitchOpenHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	uri := st.SettingsState.TwitchAuth.VerificationURI
	if uri != "" {
		if err := browser.OpenURL(uri); err != nil {
			fmt.Println("twitch: open browser:", err)
		}
	}
	ctx.Consume()
}
