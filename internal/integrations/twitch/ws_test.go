package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsServer — тестовый EventSub-сервер: поднимает ws-апгрейдер и вызывает
// handler на каждое подключение.
type wsServer struct {
	srv  *httptest.Server
	url  string
	mu   sync.Mutex
	dial int // число подключений
}

func newWSServer(t *testing.T, handler func(t *testing.T, ws *websocket.Conn, dial int)) *wsServer {
	t.Helper()
	up := websocket.Upgrader{}
	s := &wsServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer ws.Close()
		s.mu.Lock()
		s.dial++
		n := s.dial
		s.mu.Unlock()
		handler(t, ws, n)
	}))
	t.Cleanup(s.srv.Close)
	s.url = "ws" + strings.TrimPrefix(s.srv.URL, "http")
	return s
}

func (s *wsServer) dials() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dial
}

// sendEvent шлёт EventSub-кадр с metadata.message_type.
func sendEvent(t *testing.T, ws *websocket.Conn, msgType string, payload any) {
	t.Helper()
	frame := map[string]any{
		"metadata": map[string]any{
			"message_type":      msgType,
			"message_id":        "mid-" + msgType,
			"message_timestamp": "2026-09-16T00:00:00Z",
		},
		"payload": payload,
	}
	if err := ws.WriteJSON(frame); err != nil {
		t.Errorf("send %s: %v", msgType, err)
	}
}

// readEvent ждёт кадр с metadata.message_type == wantType (остальные пропускает).
// Возвращает payload сырым JSON.
func readEvent(t *testing.T, ws *websocket.Conn, wantType string, timeout time.Duration) []byte {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(timeout))
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			t.Errorf("%s не получен: %v", wantType, err)
			return nil
		}
		var frame struct {
			Metadata struct {
				MessageType string `json:"message_type"`
				MessageID   string `json:"message_id"`
				Timestamp   string `json:"message_timestamp"`
			} `json:"metadata"`
			Payload json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Errorf("кадр: %v", err)
			return nil
		}
		if frame.Metadata.MessageType == wantType {
			return frame.Payload
		}
	}
}

func welcomePayload(id string, keepalive int, reconnectURL string) map[string]any {
	return map[string]any{"session": map[string]any{
		"id":                        id,
		"status":                    "connected",
		"keepalive_timeout_seconds": keepalive,
		"reconnect_url":             reconnectURL,
	}}
}

func stopEventSub(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
}

// TestWSLifecycle — e04s02-t1: жизненный цикл EventSub-соединения:
// welcome → OnSession(session_id), ping → pong, keepalive-таймаут →
// реконнект, session_reconnect → переподключение на reconnect_url.
func TestWSLifecycle(t *testing.T) {
	t.Run("welcome session", func(t *testing.T) {
		s := newWSServer(t, func(t *testing.T, ws *websocket.Conn, dial int) {
			sendEvent(t, ws, "session_welcome", welcomePayload("sess-1", 30, ""))
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, _ = ws.ReadMessage() // ждём закрытия от клиента
		})
		got := make(chan string, 1)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- NewEventSub(EventSubOptions{
				WSURL: s.url,
				OnSession: func(sessionID string, keepalive time.Duration, reconnectURL string) {
					if sessionID == "sess-1" && keepalive == 30*time.Second && reconnectURL == "" {
						select {
						case got <- sessionID:
						default:
						}
					}
				},
			}).Run(ctx)
		}()
		select {
		case id := <-got:
			if id != "sess-1" {
				t.Fatalf("OnSession: %q, want sess-1", id)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("OnSession не вызван после welcome")
		}
		stopEventSub(t, cancel, done)
	})

	t.Run("ping pong", func(t *testing.T) {
		pong := make(chan struct{}, 1)
		s := newWSServer(t, func(t *testing.T, ws *websocket.Conn, dial int) {
			sendEvent(t, ws, "session_welcome", welcomePayload("sess-1", 30, ""))
			sendEvent(t, ws, "session_ping", map[string]any{})
			readEvent(t, ws, "session_pong", 3*time.Second)
			pong <- struct{}{}
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, _ = ws.ReadMessage() // ждём закрытия от клиента
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- NewEventSub(EventSubOptions{WSURL: s.url}).Run(ctx)
		}()
		select {
		case <-pong:
		case <-time.After(3 * time.Second):
			t.Fatal("session_pong не отправлен в ответ на session_ping")
		}
		stopEventSub(t, cancel, done)
	})

	t.Run("keepalive reconnect", func(t *testing.T) {
		redial := make(chan int, 4)
		s := newWSServer(t, func(t *testing.T, ws *websocket.Conn, dial int) {
			sendEvent(t, ws, "session_welcome", welcomePayload("sess-1", 1, ""))
			if dial >= 2 {
				redial <- dial
			}
			_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
			_, _, _ = ws.ReadMessage() // ждём закрытия от клиента
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- NewEventSub(EventSubOptions{WSURL: s.url}).Run(ctx)
		}()
		select {
		case n := <-redial:
			if n < 2 {
				t.Fatalf("реконнект: dial=%d, want >=2", n)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("нет переподключения по keepalive-таймауту (dial=%d)", s.dials())
		}
		stopEventSub(t, cancel, done)
	})

	t.Run("session reconnect", func(t *testing.T) {
		s2 := newWSServer(t, func(t *testing.T, ws *websocket.Conn, dial int) {
			sendEvent(t, ws, "session_welcome", welcomePayload("sess-2", 30, ""))
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, _ = ws.ReadMessage()
		})
		var s1 *wsServer
		s1 = newWSServer(t, func(t *testing.T, ws *websocket.Conn, dial int) {
			sendEvent(t, ws, "session_welcome", welcomePayload("sess-1", 30, s2.url))
			sendEvent(t, ws, "session_reconnect", map[string]any{"session": map[string]any{
				"id":            "sess-1",
				"status":        "reconnecting",
				"reconnect_url": s2.url,
			}})
			_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _, _ = ws.ReadMessage() // старый сокет закрывается клиентом
		})
		got := make(chan string, 1)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- NewEventSub(EventSubOptions{
				WSURL: s1.url,
				OnSession: func(sessionID string, keepalive time.Duration, reconnectURL string) {
					if sessionID == "sess-2" {
						select {
						case got <- sessionID:
						default:
						}
					}
				},
			}).Run(ctx)
		}()
		select {
		case id := <-got:
			if id != "sess-2" {
				t.Fatalf("OnSession: %q, want sess-2 после session_reconnect", id)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("нет перехода на reconnect_url (dial s2=%d)", s2.dials())
		}
		stopEventSub(t, cancel, done)
	})
}
