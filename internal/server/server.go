// Package server — localhost HTTP-сервер с OBS-виджетом: страница /widget и
// SSE-канал /events (однонаправленный поток снапшотов таймера).
// Сервер слушает только 127.0.0.1 — не доступен извне (см. e01s02-widget.md).
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/EFGLab/efg-timer/internal/helpers"
	"github.com/EFGLab/efg-timer/internal/timer"
)

// heartbeatInterval — период SSE-комментариев keepalive (var — переопределяется в тестах).
var heartbeatInterval = 15 * time.Second

// snapshot — payload события «snapshot» (e01s02-t2: {state, remaining_ms, duration_ms}).
type snapshot struct {
	State       string `json:"state"`
	RemainingMs int64  `json:"remaining_ms"`
	DurationMs  int64  `json:"duration_ms"`
}

// stateName — строковое имя состояния для JSON-payload.
func stateName(s helpers.State) string {
	switch s {
	case helpers.Running:
		return "running"
	case helpers.Paused:
		return "paused"
	default:
		return "idle"
	}
}

// serverState — жизненный цикл слушателя для статусной строки UI.
type serverState int

const (
	stateNew       serverState = iota // создан, ещё не стартовал
	stateListening                    // слушает — зелёная строка
	stateFailed                       // ошибка бинда — красная с текстом
	stateStopped                      // остановлен — красная
)

// Server — обёртка над http.Server с муксом виджета.
type Server struct {
	timer *timer.Timer
	port  int
	srv   *http.Server
	mux   *http.ServeMux

	onStatus func(up bool, url string, errText string)

	mu      sync.Mutex
	state   serverState
	errText string // причина stateFailed (текст для UI)
	stopped bool   // сервер уже остановлен — повторный Start невозможен
}

// New создаёт сервер виджета на указанном порту (порт <= 0 — ошибка на старте).
// onStatus вызывается при каждой смене статуса (может быть nil): (up, url, errText).
// Начальный статус («не слушает») в колбэк не попадает — его берут через Status().
func New(tm *timer.Timer, port int, onStatus func(up bool, url string, errText string)) *Server {
	s := &Server{
		timer:    tm,
		port:     port,
		mux:      http.NewServeMux(),
		onStatus: onStatus,
		state:    stateNew,
	}

	s.mux.HandleFunc("/widget", s.handleWidget)
	s.mux.HandleFunc("/events", s.handleEvents)

	// Сервер создаётся сразу: Handler() должен работать до Start().
	s.srv = &http.Server{
		Addr:    fmt.Sprintf("127.0.0.1:%d", port),
		Handler: s.mux,
	}

	return s
}

// Handler возвращает корневой обработчик — для тестов (httptest) без бинда порта.
func (s *Server) Handler() http.Handler { return s.srv.Handler }

// URL — адрес страницы виджета для Browser Source в OBS.
func (s *Server) URL() string { return fmt.Sprintf("http://127.0.0.1:%d/widget", s.port) }

// Status — снимок для статусной строки UI: слушает ли сервер, URL страницы
// и текст ошибки (пуст при отсутствии). Безопасен из любой горутины.
func (s *Server) Status() (up bool, url string, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case stateListening:
		return true, s.URL(), ""
	case stateFailed:
		return false, s.URL(), s.errText
	default:
		return false, s.URL(), ""
	}
}

// setState фиксирует переход статуса и уведомляет onStatus (вне мьютекса:
// колбэк вправе читать Status()).
func (s *Server) setState(st serverState, errText string) {
	s.mu.Lock()
	changed := s.state != st || s.errText != errText
	s.state = st
	s.errText = errText
	s.mu.Unlock()

	if !changed || s.onStatus == nil {
		return
	}
	up, url, text := s.Status()
	s.onStatus(up, url, text)
}

// Start запускает HTTP-сервер и блокируется (вызывать в горутине).
// Лог старта идёт в stderr (стандартный log); статус доступен через Status().
func (s *Server) Start() error {
	if s.port <= 0 {
		err := fmt.Errorf("неверный порт виджета: %d", s.port)
		s.setState(stateFailed, err.Error())
		return err
	}

	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return errors.New("сервер виджета уже остановлен")
	}

	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		err = fmt.Errorf("порт %d недоступен: %w", s.port, err)
		s.setState(stateFailed, err.Error())
		return err
	}
	log.Printf("виджет доступен: %s", s.URL())
	s.setState(stateListening, "")
	err = s.srv.Serve(ln)
	s.setState(stateStopped, "")
	return err
}

// Stop корректно останавливает сервер и активные SSE-подключения.
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	err := s.srv.Shutdown(ctx)
	s.setState(stateStopped, "")
	return err
}

// handleWidget отдаёт HTML-страницу виджета (содержимое — задача t3).
func (s *Server) handleWidget(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, widgetHTML)
}

// handleEvents отдаёт SSE-поток снапшотов: подписка на тики движка,
// начальный кадр сразу при подключении, heartbeat-комментарии каждые
// heartbeatInterval; отключение клиента закрывает поток без утечки горутин.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		// net/http: «Handlers should always test for this ability at runtime» —
		// обёртки ResponseWriter могут не поддерживать Flush.
		http.Error(w, "SSE требует ResponseWriter с поддержкой http.Flusher",
			http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	// Подписка на тики: callback вызывается из горутины драйвера тиков;
	// только кладёт снапшот в буферизованный канал (медленный клиент
	// пропускает кадры, но не тормозит движок).
	events := make(chan snapshot, 8)
	un := s.timer.Subscribe(func(remaining time.Duration) {
		snap := snapshot{
			State:       stateName(s.timer.State()),
			RemainingMs: remaining.Milliseconds(),
			DurationMs:  s.timer.Duration().Milliseconds(),
		}
		select {
		case events <- snap:
		default:
		}
	})
	defer un()

	writeSnapshot := func(snap snapshot) {
		b, err := json.Marshal(snap)
		if err != nil {
			return
		}
		io.WriteString(w, "event: snapshot\ndata: "+string(b)+"\n\n")
		f.Flush()
	}
	writeSnapshot(snapshot{
		State:       stateName(s.timer.State()),
		RemainingMs: s.timer.Remaining().Milliseconds(),
		DurationMs:  s.timer.Duration().Milliseconds(),
	})
	log.Printf("SSE: подписчик подключён")
	defer log.Printf("SSE: подписчик отключён")

	hb := time.NewTicker(heartbeatInterval)
	defer hb.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-hb.C:
			io.WriteString(w, ": heartbeat\n\n")
			f.Flush()
		case snap := <-events:
			writeSnapshot(snap)
		}
	}
}

// widgetHTML — страница виджета для OBS Browser Source: подписка на SSE,
// перерисовка MM:SS без перезагрузки, прозрачный фон для наложения на сцену.
// Скругление вниз (floor) — паритет с FormatMMSS десктоп-окна.
const widgetHTML = `<!doctype html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>TimerWidget</title>
<style>
  html, body { background: transparent; margin: 0; }
  #remaining {
    font: 700 96px/1.1 system-ui, sans-serif;
    color: #fff;
    text-shadow: 0 2px 8px rgba(0, 0, 0, 0.6);
  }
</style>
</head>
<body>
<div id="remaining">--:--</div>
<script>
(function () {
  var el = document.getElementById('remaining');
  function pad(n) { return (n < 10 ? '0' : '') + n; }
  function render(p) {
    var s = Math.max(0, Math.floor(p.remaining_ms / 1000));
    el.textContent = pad(Math.floor(s / 60)) + ':' + pad(s % 60);
  }
  var es = new EventSource('/events');
  es.addEventListener('snapshot', function (e) {
    render(JSON.parse(e.data));
  });
})();
</script>
</body>
</html>
`
