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

const counterFontSize = 220

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

	applyRulesToStore(appState)

	appState.Timer = timer.New(appState.Settings.DefaultDuration.ToDuration())

	restoreAuditLog(appState.Timer)

	appState.RootState.Remaining = appState.Timer.Remaining()
	appState.RootState.TimerState = appState.Timer.State()

	appState.Timer.Subscribe(func(remaining time.Duration) {
		publishRemaining(remaining)
	})

	gui.Stream(w, tickChannel, func(w *gui.Window, remaining time.Duration) {
		s := gui.State[state.AppState](w)
		s.RootState.Remaining = remaining
		s.RootState.TimerState = s.Timer.State()
	})

	twitchProvider := providers.NewTwitchProvider(providers.TwitchOptions{
		ClientID: settings.TwitchClientID,
		Store:    guiSecretStore{w: w},
	})

	daProvider := providers.NewDonationAlertsProvider(providers.DonationAlertsOptions{
		ClientID: settings.DonationAlertsClientID,
		Store:    guiSecretStore{w: w},
	})
	appState.Providers = []providers.Provider{
		daProvider,
		twitchProvider,
	}

	go appState.Timer.Run(w.Ctx())

	startServer(w, appState)

	if pprofEnabled() {
		startPprof()
	}

	w.SetView(rootView)

	twitchCfg := appState.Settings.Providers.Twitch
	go restoreTwitchSession(w, twitchProvider, twitchCfg)

	daCfg := appState.Settings.Providers.DonationAlerts
	go restoreDonationAlertsSession(w, daProvider, daCfg)
}

const auditRestoreLimit = 200

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

var tickChannel = make(chan time.Duration, 1)

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

const pprofEnv = "EFG_TIMER_PPROF"
const pprofAddr = "localhost:6060"

func pprofEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(pprofEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

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

func FormatMMSS(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int64(d / time.Second)
	return fmt.Sprintf("%02d:%02d", s/60, s%60)
}

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
	ctx.Consume()

	ctx.Window.DialogDismiss()
}
