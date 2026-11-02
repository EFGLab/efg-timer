package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/EFGLab/efg-timer/internal/server"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
	"github.com/pkg/errors"
)

const defaultServerPort = 8089

var serverStatusChannel = make(chan state.ServerStatus, 1)

func startServer(w *gui.Window, appState *state.AppState) {
	port := appState.Settings.Port
	if port <= 0 {
		port = defaultServerPort
	}

	srv := server.New(appState.Timer, port, func(up bool, url string, errText string) {
		publishServerStatus(state.ServerStatus{Up: up, URL: url, Err: errText})
	})

	gui.Stream(w, serverStatusChannel, func(w *gui.Window, st state.ServerStatus) {
		s := gui.State[state.AppState](w)
		s.RootState.ServerStatus = st
	})

	go func() {
		err := srv.Start()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Println(errors.Wrap(err, "server.Start"))
		}
	}()

	go func() {
		<-w.Ctx().Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Stop(ctx); err != nil {
			fmt.Println(errors.Wrap(err, "server.Stop"))
		}
	}()
}

func publishServerStatus(st state.ServerStatus) {
	select {
	case serverStatusChannel <- st:
	default:
	}
}
