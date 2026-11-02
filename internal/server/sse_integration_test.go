package server

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/timer"
)

// Task e01s02-t4 (e01s02-widget.md §6, §15–§17, §19): фан-аут на трёх
// подписчиков, изоляция обрыва, возврат горутин к исходному; плюс §16 —
// логи подключения/отключения подписчика в stderr.

type sseClient struct {
	resp   *http.Response
	reader *bufio.Reader
}

func connectSSE(t *testing.T, client *http.Client, baseURL string) *sseClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() }) // LIFO: закрытие тела до отмены ctx
	return &sseClient{resp: resp, reader: bufio.NewReader(resp.Body)}
}

// nextEvent блокируется до очередного SSE-события (страхование — ctx 10s).
func (c *sseClient) nextEvent(t *testing.T) (event, data string) {
	t.Helper()
	return readEvent(t, c.reader)
}

// waitGoroutines ждёт возврата числа горутин к исходному (§19: тест на утечку).
// Пауза здесь — опрос асинхронного закрытия соединений, а не ожидание вычислений.
func waitGoroutines(t *testing.T, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("горутины не вернулись к исходному: было %d, стало %d",
		baseline, runtime.NumGoroutine())
}

// integrationEnv — общая обстановка: тестовый сервер, транспорт клиента без
// keep-alive (чистый выход горутин клиента). Таймер намеренно не запущен:
// запуск делает вызывающий (см. startTimer) — так кадры подписчиков
// детерминированы, часы не заморожены.
func integrationEnv(t *testing.T) (*timer.Timer, *httptest.Server, *http.Client) {
	t.Helper()
	old := heartbeatInterval
	heartbeatInterval = time.Hour // без heartbeat-шума в интеграционном сценарии
	t.Cleanup(func() { heartbeatInterval = old })

	tm := timer.New(time.Minute)

	ts := httptest.NewServer(New(tm, 8089, nil).Handler())
	t.Cleanup(ts.Close)

	tr := &http.Transport{DisableKeepAlives: true}
	client := &http.Client{Transport: tr}
	t.Cleanup(client.CloseIdleConnections)
	return tm, ts, client
}

// startTimer запускает движок в горутине и ждёт состояния running.
func startTimer(t *testing.T, tm *timer.Timer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go tm.Run(ctx)
	waitRunning(t, tm)
}

func TestSSEIntegration(t *testing.T) {
	tm, ts, client := integrationEnv(t)
	startTimer(t, tm)
	baseline := runtime.NumGoroutine()

	// 1) три подписчика получают одинаковый начальный кадр
	clients := make([]*sseClient, 3)
	for i := range clients {
		clients[i] = connectSSE(t, client, ts.URL)
	}
	for i, c := range clients {
		event, data := c.nextEvent(t)
		if event != "snapshot" {
			t.Fatalf("клиент %d: первое событие %q, want snapshot", i, event)
		}
		var p snapshot
		if err := json.Unmarshal([]byte(data), &p); err != nil {
			t.Fatalf("клиент %d: payload %q: %v", i, data, err)
		}
		if p.State != "running" || p.RemainingMs != 60000 || p.DurationMs != 60000 {
			t.Errorf("клиент %d: начальный кадр %+v", i, p)
		}
	}

	// тик доходит до всех подписчиков одинаковым (остаток уменьшается раз в секунду)
	tickData := ""
	for i, c := range clients {
		event, data := c.nextEvent(t)
		if event != "snapshot" {
			t.Fatalf("клиент %d: событие %q, want snapshot", i, event)
		}
		if i == 0 {
			tickData = data
		} else if data != tickData {
			t.Errorf("клиент %d: payload %q != эталона %q", i, data, tickData)
		}
	}
	var p snapshot
	if err := json.Unmarshal([]byte(tickData), &p); err != nil {
		t.Fatalf("tick payload %q: %v", tickData, err)
	}
	if p.State != "running" || p.RemainingMs != 59000 || p.DurationMs != 60000 {
		t.Errorf("тик: %+v", p)
	}

	// 2) обрыв одного соединения безопасен: остальные живут, новые подключаются
	if err := clients[0].resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(clients); i++ {
		_, data := clients[i].nextEvent(t)
		if !strings.Contains(data, "58000") {
			t.Errorf("выживший клиент %d: payload %q, want remaining_ms 58000", i, data)
		}
	}
	fresh := connectSSE(t, client, ts.URL)
	event, data := fresh.nextEvent(t)
	if event != "snapshot" {
		t.Errorf("новый подписчик: событие %q, want snapshot", event)
	}
	var fp snapshot
	if err := json.Unmarshal([]byte(data), &fp); err != nil {
		t.Fatalf("новый подписчик: payload %q: %v", data, err)
	}
	if fp.State != "running" || fp.DurationMs != 60000 {
		t.Errorf("новый подписчик: кадр %+v", fp)
	}
	// значение на момент подключения: 58000, если следующий тик ещё не был,
	// иначе 57000 (тик раз в секунду)
	if fp.RemainingMs < 57000 || fp.RemainingMs > 58000 {
		t.Errorf("новый подписчик: remaining_ms = %d, want 57000..58000", fp.RemainingMs)
	}

	// 3) после отключения всех клиентов горутины возвращаются к исходному
	for _, c := range []*sseClient{clients[1], clients[2], fresh} {
		_ = c.resp.Body.Close()
	}
	waitGoroutines(t, baseline)
}

// syncLogBuf — потокобезопасный захват лога: пишет из горутин обработчиков
// (log.Printf), читает из тестовой горутины — strings.Builder сам по себе не
// потокобезопасен (ловилось race detector'ом).
type syncLogBuf struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *syncLogBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncLogBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func TestSSESubscriberLogs(t *testing.T) {
	_, ts, client := integrationEnv(t)

	logs := &syncLogBuf{}
	log.SetOutput(logs)
	defer log.SetOutput(os.Stderr)

	c := connectSSE(t, client, ts.URL)
	_, _ = c.nextEvent(t) // начальный кадр получен — соединение живо
	_ = c.resp.Body.Close()

	// отключение фиксируется асинхронно — опрос лога, а не ожидание вычислений
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out := logs.String()
		if strings.Contains(out, "подключ") && strings.Contains(out, "отключ") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("в логах нет подключения/отключения подписчика: %q", logs.String())
}
