// Тесты SignalR-клиента Donatex (e03s01-t2): приём DonationCreated,
// игнор DonationRerun, токен в query negotiate-запроса, лимит
// переподключений ≤2 подкл./сек. Mock-хаб — серверная часть самой
// библиотеки philippseith/signalr на httptest.
package donatex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/philippseith/signalr"
)

// fakeEvent — событие, которое хаб рассылает при подключении клиента.
type fakeEvent struct {
	target  string
	payload map[string]any
}

// fakeHub — тестовый хаб: при OnConnected шлёт заготовленные события.
// Hub встраивается ПО ЗНАЧЕНИЮ (как в тестах библиотеки): SimpleHubFactory/
// reflect.New создают нулевой экземпляр, где указатель *Hub был бы nil —
// паника. События передаются через HubFactory, а не через прототип.
type fakeHub struct {
	signalr.Hub
	events []fakeEvent
}

func (h *fakeHub) OnConnected(connID string) {
	for _, e := range h.events {
		h.Clients().All().Send(e.target, e.payload)
	}
}

// startMockHub поднимает SignalR-сервер на httptest; возвращает URL хаба
// и канал, в который пишется access_token из каждого negotiate-запроса.
func startMockHub(t *testing.T, hub *fakeHub) (string, <-chan string) {
	t.Helper()
	mux := http.NewServeMux()
	srv, err := signalr.NewServer(context.Background(), signalr.HubFactory(func() signalr.HubInterface {
		return &fakeHub{events: hub.events}
	}))
	if err != nil {
		t.Fatal(err)
	}
	srv.MapHTTP(signalr.WithHTTPServeMux(mux), "/hub")
	tokens := make(chan string, 8)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/negotiate") {
			tokens <- r.URL.Query().Get("access_token")
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts.URL + "/hub", tokens
}

func TestClientReceivesDonationCreated(t *testing.T) {
	hub := &fakeHub{events: []fakeEvent{{
		target: "DonationCreated",
		payload: map[string]any{
			"id": "d1", "amount": 100.0, "currency": "RUB", "amountInRub": 100.0,
			"username": "Alice", "message": "вперёд", "isTest": false,
		},
	}}}
	url, _ := startMockHub(t, hub)
	got := make(chan Donation, 4)
	c := newClient(url, "tok-1", func(d Donation) { got <- d })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	select {
	case d := <-got:
		if d.ID != "d1" || d.AmountInRub != 100 || d.Username != "Alice" || d.Message != "вперёд" || d.IsTest {
			t.Fatalf("Donation = %+v", d)
		}
	case <-ctx.Done():
		t.Fatal("DonationCreated не получен")
	}
}

// DonationRerun — повторный показ доната; не должен дойти до колбэка.
func TestClientIgnoresDonationRerun(t *testing.T) {
	hub := &fakeHub{events: []fakeEvent{
		{target: "DonationRerun", payload: map[string]any{"id": "old", "amountInRub": 999.0}},
		{target: "DonationCreated", payload: map[string]any{"id": "new", "amountInRub": 42.0}},
	}}
	url, _ := startMockHub(t, hub)
	got := make(chan Donation, 4)
	c := newClient(url, "tok", func(d Donation) { got <- d })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	select {
	case d := <-got:
		if d.ID != "new" {
			t.Fatalf("прошёл rerun: %+v", d)
		}
	case <-ctx.Done():
		t.Fatal("DonationCreated не получен")
	}
	select {
	case d := <-got:
		t.Fatalf("лишний колбэк: %+v", d)
	case <-time.After(300 * time.Millisecond):
	}
}

// Токен должен уйти в query negotiate-запроса (как в референсе Donatex).
func TestClientSendsTokenInQuery(t *testing.T) {
	hub := &fakeHub{events: []fakeEvent{
		{target: "DonationCreated", payload: map[string]any{"id": "d1", "amountInRub": 5.0}},
	}}
	url, tokens := startMockHub(t, hub)
	got := make(chan Donation, 4)
	c := newClient(url, "секрет-tok", func(d Donation) { got <- d })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() { _ = c.Run(ctx) }()

	select {
	case tok := <-tokens:
		if tok != "секрет-tok" {
			t.Fatalf("access_token = %q, want %q", tok, "секрет-tok")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("negotiate не дошёл до хаба")
	}
}

// Лимитер допускает подключения не чаще одного раза за interval:
// резервации чистые (подставные часы) — без реальных пауз.
func TestRateLimiterTwoPerSecond(t *testing.T) {
	cur := time.Unix(0, 0)
	l := newRateLimiter(500*time.Millisecond, func() time.Time { return cur })

	if d := l.reserve(); d != 0 {
		t.Fatalf("первое подключение: wait=%v, want 0", d)
	}
	for i, want := range []time.Duration{500 * time.Millisecond, time.Second, 1500 * time.Millisecond} {
		if d := l.reserve(); d != want {
			t.Fatalf("reserve #%d = %v, want %v", i+2, d, want)
		}
	}
	cur = cur.Add(2 * time.Second)
	if d := l.reserve(); d != 0 {
		t.Fatalf("после паузы: wait=%v, want 0", d)
	}
	if d := l.reserve(); d != 500*time.Millisecond {
		t.Fatalf("снова подряд: %v, want 500ms", d)
	}
}
