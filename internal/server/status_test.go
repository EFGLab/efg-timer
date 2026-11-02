package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/timer"
)

// Task e01s02-t6 (дельта владельца): UI показывает статус сервера —
// Status() отдаёт снимок (слушает / остановлен / ошибка бинда) для
// статусной строки окна.

// freePort возвращает свободный порт на 127.0.0.1: тесты, которые реально
// биндят порт, не должны зависеть от дефолтного 8089 — его может держать
// запущенное приложение.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("найти свободный порт: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// waitForUp опрашивает асинхронный переход статуса (пауза — ожидание
// перехода, не вычислений).
func waitForUp(t *testing.T, s *Server, want bool) (bool, string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		up, _, errText := s.Status()
		if up == want {
			return up, errText
		}
		time.Sleep(10 * time.Millisecond)
	}
	up, _, errText := s.Status()
	return up, errText
}

func TestServerStatusLifecycle(t *testing.T) {
	port := freePort(t)
	s := New(timer.New(time.Minute), port, nil)

	// до старта: не слушает, но URL известен (строка «остановлен» красным)
	up, url, errText := s.Status()
	if up {
		t.Error("до Start Status.up = true")
	}
	if want := fmt.Sprintf("http://127.0.0.1:%d/widget", port); url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
	if errText != "" {
		t.Errorf("errText = %q, want пусто", errText)
	}

	// старт: listening, без ошибок
	go func() { _ = s.Start() }()
	up, errText = waitForUp(t, s, true)
	if !up {
		t.Fatal("после Start сервер не в статусе listening")
	}
	if errText != "" {
		t.Errorf("errText = %q, want пусто", errText)
	}

	// остановка: stopped
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	up, errText = waitForUp(t, s, false)
	if up {
		t.Fatal("после Stop сервер всё ещё listening")
	}
	if errText != "" {
		t.Errorf("после Stop errText = %q, want пусто", errText)
	}
}

func TestServerStatusReportsBindError(t *testing.T) {
	// посторонний слушатель занимает порт — Start падает, статус обязан
	// объяснить причину (красная строка в UI)
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("занять порт: %v", err)
	}
	defer blocker.Close()
	port := blocker.Addr().(*net.TCPAddr).Port

	s := New(timer.New(time.Minute), port, nil)
	if err := s.Start(); err == nil {
		t.Fatal("Start на занятом порту не вернул ошибку")
	}
	_, _, errText := s.Status()
	if !strings.Contains(errText, strconv.Itoa(port)) {
		t.Errorf("errText = %q, want упоминание порта/причины", errText)
	}
}
