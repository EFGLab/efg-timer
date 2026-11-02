package twitch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type subPost struct {
	hdr  http.Header
	body map[string]any
}

func waitPosts(t *testing.T, mu *sync.Mutex, posts *[]subPost, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		mu.Lock()
		got := len(*posts)
		mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("подписок %d, want >= %d", got, n)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestSubscribe — e04s02-t2: подписки channel.subscribe и
// channel.subscription.message (version 1) через Helix POST в течение 10с
// после каждого welcome (в т.ч. после session_reconnect).
func TestSubscribe(t *testing.T) {
	var mu sync.Mutex
	var posts []subPost
	var wsDial int
	var wsMu sync.Mutex
	var up websocket.Upgrader

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer ws.Close()
		wsMu.Lock()
		wsDial++
		dial := wsDial
		wsMu.Unlock()
		if dial == 1 {
			sendEvent(t, ws, "session_welcome", welcomePayload("sess-1", 30, ""))
			sendEvent(t, ws, "session_reconnect", map[string]any{"session": map[string]any{
				"id":            "sess-1",
				"status":        "reconnecting",
				"reconnect_url": "ws" + strings.TrimPrefix(srvURLFrom(r), "http") + "/ws",
			}})
		} else {
			sendEvent(t, ws, "session_welcome", welcomePayload("sess-2", 30, ""))
		}
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, _ = ws.ReadMessage() // до закрытия клиентом
	})
	mux.HandleFunc("/helix/eventsub/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		mu.Lock()
		posts = append(posts, subPost{hdr: r.Header.Clone(), body: m})
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- NewEventSub(EventSubOptions{
			WSURL:         wsURL,
			Client:        client,
			BroadcasterID: "123",
		}).Run(ctx)
	}()

	// 2 подписки после первого welcome, ещё 2 после reconnect (sess-2)
	waitPosts(t, &mu, &posts, 2, 3*time.Second)
	waitPosts(t, &mu, &posts, 4, 5*time.Second)
	stopEventSub(t, cancel, done)

	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 4 {
		t.Fatalf("POST-ов %d, want 4", len(posts))
	}
	types := map[string]int{}
	sessions := map[string]int{}
	for _, p := range posts {
		if got := p.hdr.Get("Client-Id"); got != "cid" {
			t.Errorf("Client-Id = %q, want cid", got)
		}
		if got := p.hdr.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want Bearer tok", got)
		}
		if v, _ := p.body["version"].(string); v != "1" {
			t.Errorf("version = %v, want 1", p.body["version"])
		}
		typ, _ := p.body["type"].(string)
		types[typ]++
		tr, _ := p.body["transport"].(map[string]any)
		if tr == nil || tr["method"] != "websocket" {
			t.Errorf("transport = %v, want method websocket", p.body["transport"])
		}
		sid, _ := tr["session_id"].(string)
		sessions[sid]++
		cond, _ := p.body["condition"].(map[string]any)
		if cond == nil || cond["broadcaster_user_id"] != "123" {
			t.Errorf("condition = %v, want broadcaster_user_id 123", p.body["condition"])
		}
	}
	if types["channel.subscribe"] != 2 || types["channel.subscription.message"] != 2 {
		t.Errorf("типы подписок: %v, want по 2 каждого", types)
	}
	if sessions["sess-1"] != 2 || sessions["sess-2"] != 2 {
		t.Errorf("session_id: %v, want по 2 на sess-1 и sess-2 (повтор после reconnect)", sessions)
	}
}

// srvURLFrom — host:port из запроса для сборки ws-URL reconnect_url в моке.
func srvURLFrom(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
