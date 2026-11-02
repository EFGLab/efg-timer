package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/helpers"
	"github.com/EFGLab/efg-timer/internal/timer"
)

// Task e01s02-t2 (P0): payload {state, remaining_ms, duration_ms}; heartbeat-
// комментарий каждые 15 сек; рантайм-проверка http.Flusher (net/http:
// «Handlers should always test for this ability at runtime»).

// readEvent читает SSE-событие до пустой строки-разделителя.
func readEvent(t *testing.T, rd *bufio.Reader) (event string, data string) {
	t.Helper()
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("read event: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		case line == "":
			if event != "" || data != "" {
				return event, data
			}
		}
	}
}

// waitRunning ждёт состояния running: тикер движка поднимается асинхронно.
func waitRunning(t *testing.T, tm *timer.Timer) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tm.State() == helpers.Running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("таймер не перешёл в состояние running (состояние %d)", tm.State())
}

func TestSSEPayloadSnapshot(t *testing.T) {
	tm := timer.New(65 * time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tm.Run(ctx)
	waitRunning(t, tm)

	s := New(tm, 8089, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	reqCtx, reqCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer reqCancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	event, data := readEvent(t, bufio.NewReader(resp.Body))
	if event != "snapshot" {
		t.Fatalf("event = %q, want snapshot", event)
	}

	var payload struct {
		State       string `json:"state"`
		RemainingMs int64  `json:"remaining_ms"`
		DurationMs  int64  `json:"duration_ms"`
	}
	if err := json.Unmarshal([]byte(data), &payload); err != nil {
		t.Fatalf("payload %q: %v", data, err)
	}
	if payload.State != "running" {
		t.Errorf("state = %q, want running", payload.State)
	}
	if payload.RemainingMs != 65000 {
		t.Errorf("remaining_ms = %d, want 65000", payload.RemainingMs)
	}
	if payload.DurationMs != 65000 {
		t.Errorf("duration_ms = %d, want 65000", payload.DurationMs)
	}
}

func TestSSEHeartbeatComment(t *testing.T) {
	old := heartbeatInterval
	heartbeatInterval = 30 * time.Millisecond // ускоряем для теста
	defer func() { heartbeatInterval = old }()

	s := New(timer.New(time.Minute), 8089, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	rd := bufio.NewReader(resp.Body)
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("heartbeat не получен за таймаут: %v", err)
		}
		if strings.TrimSpace(line) == ": heartbeat" {
			return // найден комментарий-heartbeat
		}
	}
}

// noFlushWriter — обёртка без http.Flusher: SSE обязан ответить 500
// с объяснением, а не зависнуть или запаниковать.
type noFlushWriter struct{ http.ResponseWriter }

func TestSSERequiresFlusherRuntimeCheck(t *testing.T) {
	s := New(timer.New(time.Minute), 8089, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)

	s.handleEvents(noFlushWriter{rec}, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Flusher") {
		t.Errorf("body = %q, want объяснение про Flusher", body)
	}
}
