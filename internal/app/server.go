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

// defaultServerPort — порт виджета, если в настройках он пустой.
const defaultServerPort = 8089

// serverStatusChannel — канал публикаций статуса сервера в окно.
var serverStatusChannel = make(chan state.ServerStatus, 1)

// startServer поднимает HTTP-сервер виджета: страница /widget для OBS
// Browser Source и SSE-канал /events. Статус сервера публикуется в
// статусную строку окна.
//
// Сервер живёт в фоновой горутине, а UI читает только состояние окна:
// статус приходит через Stream (frame thread), а по закрытию окна сервер
// останавливается.
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
		// ErrServerClosed — штатная остановка при закрытии окна, не ошибка.
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

// publishServerStatus публикует статус сервера в окно. Отправка
// неблокирующая: вызывает фоновая горутина сервера.
func publishServerStatus(st state.ServerStatus) {
	select {
	case serverStatusChannel <- st:
	default:
	}
}
