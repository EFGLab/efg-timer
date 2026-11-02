package twitch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/gorilla/websocket"
)

// TestE2E — e04s02-t4: сквозной пайплайн против мока протокола Twitch:
// welcome → подписки → notification (саб tier 3000 + ре-саб) → таймер вырос
// на 300с + 45с.
func TestE2E(t *testing.T) {
	var mu sync.Mutex
	var posts int
	var up websocket.Upgrader

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer ws.Close()
		sendEvent(t, ws, "session_welcome", welcomePayload("sess-1", 30, ""))
		sendEvent(t, ws, "notification", map[string]any{
			"subscription": map[string]any{"type": "channel.subscribe", "version": "1", "status": "enabled"},
			"event":        map[string]any{"broadcaster_user_id": "123", "tier": "3000", "is_gift": false},
		})
		sendEvent(t, ws, "notification", map[string]any{
			"subscription": map[string]any{"type": "channel.subscription.message", "version": "1", "status": "enabled"},
			"event":        map[string]any{"broadcaster_user_id": "123", "tier": "1000", "cumulative_months": 3},
		})
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, _ = ws.ReadMessage() // до закрытия клиентом
	})
	mux.HandleFunc("/helix/eventsub/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		posts++
		mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	client := NewClient(ClientOptions{
		ClientID:         "cid",
		Session:          TwitchSession{AccessToken: "tok"},
		SubscriptionsURL: srv.URL + "/helix/eventsub/subscriptions",
		HTTPClient:       srv.Client(),
	})
	tm := timer.New(10 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := RunSubs(ctx,
		EventSubOptions{WSURL: wsURL, Client: client, BroadcasterID: "123"},
		testSubRule(),
		func(d time.Duration) { tm.Add(timer.SourceTwitch, d, "sub") },
		nil,
	)
	if err != nil {
		t.Fatalf("RunSubs: %v", err)
	}

	mu.Lock()
	n := posts
	mu.Unlock()
	if n != 2 {
		t.Errorf("подписок %d, want 2", n)
	}

	rem := tm.Remaining()
	// база 10с + 300с (tier 3000) + 60с (ре-саб tier 1000 — по своему тиру)
	if rem < 369*time.Second || rem >= 371*time.Second {
		t.Fatalf("Remaining = %v, want ~370s — таймер не вырос на сабы", rem)
	}
}
