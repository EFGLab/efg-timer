package providers

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

// eventSubFake — фейковый Twitch EventSub (e11s02-t1): WS-эндпоинт отдаёт
// welcome и уведомления, Helix-эндпоинт подписок отвечает 202, token
// endpoint — новым токеном.
type eventSubFake struct {
	server *httptest.Server
	frames chan map[string]any

	upgrader websocket.Upgrader

	mu        sync.Mutex
	subscribe []string // Authorization подписок
	refreshes int
}

func newEventSubFake(t *testing.T) *eventSubFake {
	t.Helper()

	f := &eventSubFake{
		frames:   make(chan map[string]any, 16),
		upgrader: websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/eventsub", f.handleWS)
	mux.HandleFunc("/eventsub/subscriptions", f.handleSubscribe)
	mux.HandleFunc("/token", f.handleToken)

	f.server = httptest.NewServer(mux)
	// Порядок cleanup обратный (LIFO): сначала закрываем канал кадров
	// (WS-обработчик выйдет из цикла), потом сервер.
	t.Cleanup(f.server.Close)
	t.Cleanup(func() { close(f.frames) })

	return f
}

// wsURL — адрес WS-эндпоинта фейкового сервера.
func (f *eventSubFake) wsURL() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http") + "/eventsub"
}

// subscribeURL — адрес Helix-эндпоинта подписок.
func (f *eventSubFake) subscribeURL() string {
	return f.server.URL + "/eventsub/subscriptions"
}

// tokenURL — адрес token endpoint.
func (f *eventSubFake) tokenURL() string {
	return f.server.URL + "/token"
}

func (f *eventSubFake) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *eventSubFake) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := f.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Клиент закрыл соединение — читающий фиксирует это и поднимает closed.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	welcome := map[string]any{
		"metadata": map[string]any{
			"message_type":      "session_welcome",
			"message_id":        "welcome-1",
			"message_timestamp": time.Now().UTC().Format(time.RFC3339),
		},
		"payload": map[string]any{
			"session": map[string]any{
				"id":                        "session-1",
				"status":                    "connected",
				"keepalive_timeout_seconds": 10,
			},
		},
	}
	if err := conn.WriteJSON(welcome); err != nil {
		return
	}

	for {
		select {
		case <-closed:
			return
		case msg, ok := <-f.frames:
			if !ok {
				return
			}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		}
	}
}

func (f *eventSubFake) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.subscribe = append(f.subscribe, r.Header.Get("Authorization"))
	f.mu.Unlock()
	f.writeJSON(w, http.StatusAccepted, []any{})
}

func (f *eventSubFake) handleToken(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.refreshes++
	f.mu.Unlock()
	f.writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  "access-2",
		"refresh_token": "refresh-2",
		"expires_in":    14400,
		"token_type":    "bearer",
	})
}

func (f *eventSubFake) subscribeAuth() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.subscribe...)
}

func (f *eventSubFake) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

// waitSubscribe — ждёт, пока подписка уйдёт на Helix-эндпоинт.
func waitSubscribe(t *testing.T, f *eventSubFake) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		auths := f.subscribeAuth()
		if len(auths) > 0 {
			return auths
		}
		if time.Now().After(deadline) {
			t.Fatal("подписка EventSub не отправлена")
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// subNotification — уведомление channel.subscribe.
func subNotification(tier string, isGift bool) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"message_type":      "notification",
			"message_id":        "n-sub",
			"message_timestamp": time.Now().UTC().Format(time.RFC3339),
		},
		"payload": map[string]any{
			"subscription": map[string]any{
				"id":     "sub-1",
				"type":   "channel.subscribe",
				"status": "enabled",
			},
			"event": map[string]any{
				"tier":    tier,
				"is_gift": isGift,
			},
		},
	}
}

// resubNotification — уведомление channel.subscription.message.
func resubNotification(tier string, months int64) map[string]any {
	return map[string]any{
		"metadata": map[string]any{
			"message_type":      "notification",
			"message_id":        "n-resub",
			"message_timestamp": time.Now().UTC().Format(time.RFC3339),
		},
		"payload": map[string]any{
			"subscription": map[string]any{
				"id":     "sub-2",
				"type":   "channel.subscription.message",
				"status": "enabled",
			},
			"event": map[string]any{
				"tier":              tier,
				"cumulative_months": months,
			},
		},
	}
}

// twitchTestEvent — событие, пойманное обработчиком On.
type twitchTestEvent struct {
	typ  EventType
	data TwitchSubscriptionEvent
}

// newTwitchEventsProvider — провайдер с фейковыми эндпоинтами и сессией в
// хранилище.
func newTwitchEventsProvider(t *testing.T, f *eventSubFake, session TwitchSession) (*TwitchProvider, *memorySecretStore) {
	t.Helper()

	store := newMemorySecretStore()
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(TwitchSessionSecretKey, data); err != nil {
		t.Fatal(err)
	}

	p := NewTwitchProvider(TwitchOptions{
		ClientID:             "client-1",
		Store:                store,
		HTTPClient:           f.server.Client(),
		TokenURL:             f.tokenURL(),
		UsersURL:             f.server.URL + "/users",
		EventSubURL:          f.wsURL(),
		EventSubSubscribeURL: f.subscribeURL(),
	})
	return p, store
}

// waitEvent — ждёт событие от обработчика (с таймаутом).
func waitEvent(t *testing.T, ch <-chan twitchTestEvent) twitchTestEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("событие не пришло")
		return twitchTestEvent{}
	}
}

// TestTwitchEventsSubscription — саб из EventSub доходит до обработчика On.
func TestTwitchEventsSubscription(t *testing.T) {
	f := newEventSubFake(t)

	p, _ := newTwitchEventsProvider(t, f, TwitchSession{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	events := make(chan twitchTestEvent, 8)
	p.On(func(typ EventType, data any) {
		ev, ok := data.(TwitchSubscriptionEvent)
		if !ok {
			t.Errorf("данные события = %T, want TwitchSubscriptionEvent", data)
			return
		}
		events <- twitchTestEvent{typ: typ, data: ev}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.StartEvents(ctx, "42")
	}()

	// Подаренный саб от купленного не отличаем: событие обычное, тир тот же.
	f.frames <- subNotification("1000", true)

	ev := waitEvent(t, events)
	if ev.typ != EventTypeSubscription {
		t.Fatalf("тип события = %q, want %q", ev.typ, EventTypeSubscription)
	}
	if ev.data.Tier != "1000" {
		t.Errorf("tier = %q, want 1000", ev.data.Tier)
	}
	if ev.data.Resub {
		t.Error("resub = true, want false")
	}
	if ev.data.CumulativeMonths != 0 {
		t.Errorf("cumulative_months = %d, want 0", ev.data.CumulativeMonths)
	}

	// Подписка на канал должна была уйти на Helix-эндпоинт.
	auths := waitSubscribe(t, f)
	if auths[0] != "Bearer access-1" {
		t.Errorf("Authorization подписки = %q, want Bearer access-1", auths[0])
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("StartEvents вернул %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartEvents не завершился после отмены")
	}
}

// TestTwitchEventsResub — ре-саб приходит тем же типом, но с Resub и
// счётчиком месяцев.
func TestTwitchEventsResub(t *testing.T) {
	f := newEventSubFake(t)

	p, _ := newTwitchEventsProvider(t, f, TwitchSession{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	events := make(chan twitchTestEvent, 8)
	p.On(func(typ EventType, data any) {
		ev, ok := data.(TwitchSubscriptionEvent)
		if !ok {
			t.Errorf("данные события = %T, want TwitchSubscriptionEvent", data)
			return
		}
		events <- twitchTestEvent{typ: typ, data: ev}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.StartEvents(ctx, "42")
	}()

	f.frames <- resubNotification("2000", 3)

	ev := waitEvent(t, events)
	if ev.typ != EventTypeSubscription {
		t.Fatalf("тип события = %q, want %q", ev.typ, EventTypeSubscription)
	}
	if !ev.data.Resub {
		t.Error("resub = false, want true")
	}
	if ev.data.CumulativeMonths != 3 {
		t.Errorf("cumulative_months = %d, want 3", ev.data.CumulativeMonths)
	}
	if ev.data.Tier != "2000" {
		t.Errorf("tier = %q, want 2000", ev.data.Tier)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("StartEvents вернул %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartEvents не завершился после отмены")
	}
}

// TestTwitchEventsStop — StopEvents останавливает поток, EventsRunning
// отражает состояние потока.
func TestTwitchEventsStop(t *testing.T) {
	f := newEventSubFake(t)

	p, _ := newTwitchEventsProvider(t, f, TwitchSession{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.StartEvents(ctx, "42")
	}()

	deadline := time.Now().Add(5 * time.Second)
	for !p.EventsRunning() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !p.EventsRunning() {
		t.Fatal("EventsRunning = false, want true во время работы потока")
	}

	p.StopEvents()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("StartEvents вернул %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartEvents не завершился после StopEvents")
	}
	if p.EventsRunning() {
		t.Error("EventsRunning = true после остановки потока")
	}
}

// TestTwitchEventsRefreshesExpiredToken — протухший access-токен обновляется
// до подключения, новая сессия сохраняется в хранилище.
func TestTwitchEventsRefreshesExpiredToken(t *testing.T) {
	f := newEventSubFake(t)

	p, store := newTwitchEventsProvider(t, f, TwitchSession{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		ExpiresAt:    time.Now().Add(-time.Minute),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.StartEvents(ctx, "42")
	}()

	deadline := time.Now().Add(5 * time.Second)
	for f.refreshCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.refreshCount() == 0 {
		t.Fatal("токен не обновлён перед подключением")
	}

	stored, found, err := store.Load(TwitchSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("обновлённая сессия не сохранена: found=%v err=%v", found, err)
	}
	var persisted TwitchSession
	if err := json.Unmarshal(stored, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.RefreshToken != "refresh-2" {
		t.Errorf("сохранён refresh = %q, want refresh-2", persisted.RefreshToken)
	}
	if persisted.ExpiresAt.Before(time.Now()) {
		t.Errorf("сохранённый срок действия в прошлом: %v", persisted.ExpiresAt)
	}

	auths := waitSubscribe(t, f)
	if auths[0] != "Bearer access-2" {
		t.Errorf("Authorization подписки = %q, want Bearer access-2", auths[0])
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("StartEvents вернул %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StartEvents не завершился после отмены")
	}
}

// TestTwitchEventsRequiresBroadcaster — без broadcaster_id поток не
// запускается: подписка EventSub без него бессмысленна.
func TestTwitchEventsRequiresBroadcaster(t *testing.T) {
	f := newEventSubFake(t)

	p, _ := newTwitchEventsProvider(t, f, TwitchSession{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	})

	if err := p.StartEvents(context.Background(), ""); err == nil {
		t.Fatal("StartEvents без broadcaster_id: want error")
	}
	if p.EventsRunning() {
		t.Error("EventsRunning = true после неудачного старта")
	}
}
