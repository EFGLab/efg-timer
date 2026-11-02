package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/timer"
)

// Task e01s02-t1: HTTP-мукс на 127.0.0.1, порт — из настроек (дефолт 8089);
// GET /widget — HTML; GET /events — SSE-заголовки; контракт Start()/URL().

func TestWidgetEndpointServesHTML(t *testing.T) {
	s := New(timer.New(time.Minute), 8089, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/widget")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /widget: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(strings.TrimSpace(string(body))) == 0 {
		t.Error("GET /widget: пустой ответ")
	}
}

func TestEventsEndpointSSEHeaders(t *testing.T) {
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

	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
}

func TestURLIsFixedWidgetAddress(t *testing.T) {
	s := New(timer.New(time.Minute), 8089, nil)
	want := "http://127.0.0.1:8089/widget"
	if got := s.URL(); got != want {
		t.Errorf("URL() = %q, want %q", got, want)
	}
}

func TestStartServesWidgetOnFixedPort(t *testing.T) {
	// порт выбирается свободным: дефолтный 8089 может держать запущенное
	// приложение — тест проверяет наш сервер, а не чужой процесс
	port := freePort(t)
	s := New(timer.New(time.Minute), port, nil)
	go func() { _ = s.Start() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer func() { _ = s.Stop(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(s.URL())
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return // сервер поднялся и отдаёт виджет
			}
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("сервер не поднялся на %s: %v", s.URL(), lastErr)
}
