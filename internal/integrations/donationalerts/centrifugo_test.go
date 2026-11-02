package donationalerts_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/integrations/donationalerts"
	"github.com/gorilla/websocket"
)

// centrifugoMock — тестовый сервер DonationAlerts для e05s02-t1: REST-подписка
// POST /api/v1/centrifuge/subscribe и WS-эндпоинт, говорящий на JSON-протоколе
// Centrifugo v1 (эпохи Centrifugo v2) — ровно как реальный сервер DA.
type centrifugoMock struct {
	srv *httptest.Server

	mu       sync.Mutex
	restAuth string
	restBody struct {
		Channels []string `json:"channels"`
		Client   string   `json:"client"`
	}

	restFail   bool          // REST отвечает 500
	subscribed chan struct{} // закрывается после подтверждения WS-подписки
}

func newCentrifugoMock(t *testing.T, publish bool) *centrifugoMock {
	m := &centrifugoMock{subscribed: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/centrifuge/subscribe", m.rest)
	mux.HandleFunc("/connection/websocket", m.ws(publish))
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)
	return m
}

// rest — REST-подписка: проверяет Bearer, запоминает {channels, client},
// возвращает токен канала (как реальный DA).
func (m *centrifugoMock) rest(w http.ResponseWriter, r *http.Request) {
	if m.restFail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	m.mu.Lock()
	m.restAuth = r.Header.Get("Authorization")
	var body struct {
		Channels []string `json:"channels"`
		Client   string   `json:"client"`
	}
	err := json.NewDecoder(r.Body).Decode(&body)
	var channel, client string
	if err == nil {
		if len(body.Channels) > 0 {
			channel = body.Channels[0]
		}
		client = body.Client
		m.restBody = body
	}
	m.mu.Unlock()
	if err != nil || channel == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{
		"channels": []map[string]string{{"channel": channel, "token": "chan-token"}},
	})
	_ = client
}

// ws — Centrifugo WS: connect (method 0) отдаёт client id, subscribe (method 1)
// подтверждает подписку токеном канала; после подтверждения (опционально)
// присылает publication push с донатом во вложенном data.data.
func (m *centrifugoMock) ws(publish bool) http.HandlerFunc {
	up := websocket.Upgrader{}
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg struct {
				Method int             `json:"method"`
				ID     int             `json:"id"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(raw, &msg) != nil {
				return
			}
			switch msg.Method {
			case 0: // connect
				var p struct {
					Token string `json:"token"`
				}
				if json.Unmarshal(msg.Params, &p) != nil || p.Token != "sock-token" {
					_ = conn.WriteJSON(map[string]any{"id": msg.ID,
						"error": map[string]any{"code": 1000, "message": "bad token"}})
					return
				}
				_ = conn.WriteJSON(map[string]any{"id": msg.ID,
					"result": map[string]any{"client": "uuid-1", "version": "2.1.0"}})
			case 1: // subscribe
				var p struct {
					Channel string `json:"channel"`
					Token   string `json:"token"`
				}
				if json.Unmarshal(msg.Params, &p) != nil ||
					p.Token != "chan-token" || p.Channel != "$alerts:donation_1" {
					_ = conn.WriteJSON(map[string]any{"id": msg.ID,
						"error": map[string]any{"code": 1000, "message": "bad channel token"}})
					return
				}
				if err := conn.WriteJSON(map[string]any{"id": msg.ID,
					"result": map[string]any{"type": 1, "channel": p.Channel}}); err != nil {
					return
				}
				close(m.subscribed)
				if !publish {
					continue
				}
				// publication push (метод 1, без id): данные доната вложены
				// в data.data — так отдаёт реальный DA
				_ = conn.WriteJSON(map[string]any{
					"method": 1,
					"params": map[string]any{
						"channel": p.Channel,
						"data": map[string]any{
							"type": "donation",
							"data": map[string]any{
								"id":       5,
								"amount":   100.5,
								"currency": "RUB",
								"username": "Ivan",
								"message":  "hi",
							},
						},
					},
				})
			}
		}
	}
}

// daClient — API-клиент с Bearer-токеном, REST-подписка смотрит в мок.
func (m *centrifugoMock) daClient() *donationalerts.Client {
	return donationalerts.NewClient(donationalerts.ClientOptions{
		ClientID:     "cid",
		ClientSecret: "csecret",
		Session:      donationalerts.Session{AccessToken: "acc-token"},
		SubscribeURL: m.srv.URL + "/api/v1/centrifuge/subscribe",
	})
}

func (m *centrifugoMock) wsURL() string {
	return "ws://" + strings.TrimPrefix(m.srv.URL, "http://") + "/connection/websocket"
}

// TestCentrifugo — e05s02-t1: DA-клиент Centrifugo (JSON-протокол v1).
func TestCentrifugo(t *testing.T) {
	t.Run("connect и REST-подписка с client id", func(t *testing.T) {
		m := newCentrifugoMock(t, false)
		cf := donationalerts.NewCentrifugo(donationalerts.CentrifugoOptions{
			WSURL:       m.wsURL(),
			SocketToken: "sock-token",
			Channel:     "$alerts:donation_1",
			Client:      m.daClient(),
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- cf.Run(ctx) }()
		select {
		case <-m.subscribed:
		case <-time.After(2 * time.Second):
			t.Fatal("подписка не подтвердилась за 2 с")
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Run после отмены: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Run не завершился за 2 с")
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.restAuth != "Bearer acc-token" {
			t.Errorf("REST Authorization = %q, want Bearer acc-token", m.restAuth)
		}
		if got := strings.Join(m.restBody.Channels, ","); got != "$alerts:donation_1" {
			t.Errorf("channels = %q", got)
		}
		if m.restBody.Client != "uuid-1" {
			t.Errorf("client = %q, want uuid-1 (из WS-connect)", m.restBody.Client)
		}
	})

	t.Run("публикация доходит до OnDonation", func(t *testing.T) {
		pub := make(chan donationalerts.DonationEvent, 1)
		m := newCentrifugoMock(t, true)
		cf := donationalerts.NewCentrifugo(donationalerts.CentrifugoOptions{
			WSURL:       m.wsURL(),
			SocketToken: "sock-token",
			Channel:     "$alerts:donation_1",
			Client:      m.daClient(),
			OnDonation: func(ev donationalerts.DonationEvent) {
				select {
				case pub <- ev:
				default:
				}
			},
		})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = cf.Run(ctx) }()
		select {
		case ev := <-pub:
			if ev.ID != 5 || ev.Amount != 100.5 || ev.Currency != "RUB" ||
				ev.Username != "Ivan" || ev.Message != "hi" {
				t.Fatalf("донат %+v, want {5 100.5 RUB Ivan hi}", ev)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("публикация не пришла за 2 с")
		}
	})

	t.Run("ошибка REST прерывает Run", func(t *testing.T) {
		m := newCentrifugoMock(t, false)
		m.restFail = true
		cf := donationalerts.NewCentrifugo(donationalerts.CentrifugoOptions{
			WSURL:       m.wsURL(),
			SocketToken: "sock-token",
			Channel:     "$alerts:donation_1",
			Client:      m.daClient(),
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := cf.Run(ctx)
		if err == nil {
			t.Fatal("Run вернул nil, want ошибка REST")
		}
		select {
		case <-m.subscribed:
			t.Fatal("WS-подписка прошла при отказе REST")
		default:
		}
	})
}
