package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// daMockDonation — донат, который фейковый сервер отправляет публикацией.
type daMockDonation struct {
	ID       int64
	Username string
	Message  string
	Amount   float64
	Currency string
}

// daEventsMock — фейковый DonationAlerts: REST-профиль, REST-подписка на канал и WS-сервер Centrifugo.
type daEventsMock struct {
	srv *httptest.Server

	mu       sync.Mutex
	userAuth string
	restAuth string
	restBody struct {
		Channels []string `json:"channels"`
		Client   string   `json:"client"`
	}

	subscribed chan struct{}      // закрывается после первой публикации
	subOnce    sync.Once          // публикации могут быть на нескольких соединениях
	rounds     [][]daMockDonation // наборы публикаций по соединениям
	dropFirst  bool               // первое соединение рвём после публикации
	conns      int32              // сколько соединений было

	// restResponse — ответ REST-подписки, если задан (имитация чужих каналов).
	restResponse []map[string]string
	// silentConnect — сервер не отвечает на connect.
	silentConnect bool
	// silentSubscribe — сервер не отвечает на subscribe.
	silentSubscribe bool
}

// newDAEventsMock — поднимает фейковый сервер DA. rounds — наборы публикаций по соединениям:
// первый набор — первому соединению, последний — всем следующим (имитация реконнекта).
func newDAEventsMock(t *testing.T, rounds ...[]daMockDonation) *daEventsMock {
	t.Helper()
	m := &daEventsMock{subscribed: make(chan struct{}), rounds: rounds}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user/oauth", m.user)
	mux.HandleFunc("/api/v1/centrifuge/subscribe", m.rest)
	mux.HandleFunc("/connection/websocket", m.ws)
	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)

	return m
}

// donationsFor — набор публикаций для соединения номер conn (нумерация с 1).
func (m *daEventsMock) donationsFor(conn int32) []daMockDonation {
	if len(m.rounds) == 0 {
		return nil
	}
	idx := int(conn) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.rounds) {
		idx = len(m.rounds) - 1
	}
	return m.rounds[idx]
}

// wsURL — WS-адрес фейкового Centrifugo.
func (m *daEventsMock) wsURL() string {
	return "ws://" + strings.TrimPrefix(m.srv.URL, "http://") + "/connection/websocket"
}

// user — профиль DA: ответ обёрнут в {"data":{…}}.
func (m *daEventsMock) user(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.userAuth = r.Header.Get("Authorization")
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"id":                      42,
			"code":                    "tester",
			"name":                    "Tester",
			"socket_connection_token": "sock-token",
		},
	})
}

// rest — REST-подписка: проверяет Bearer, запоминает {channels, client}, возвращает токен канала.
func (m *daEventsMock) rest(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.restAuth = r.Header.Get("Authorization")
	var body struct {
		Channels []string `json:"channels"`
		Client   string   `json:"client"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	m.restBody.Channels = body.Channels
	m.restBody.Client = body.Client
	m.mu.Unlock()

	channel := ""
	if len(body.Channels) > 0 {
		channel = body.Channels[0]
	}

	w.Header().Set("Content-Type", "application/json")
	if m.restResponse != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"channels": m.restResponse})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"channels": []map[string]string{{"channel": channel, "token": "chan-token"}},
	})
}

// ws — Centrifugo WS: connect (method 0) отдаёт client id, subscribe (method 1) подтверждает подписку
// токеном канала; после подтверждения присылаются публикации донатов.
func (m *daEventsMock) ws(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	n := atomic.AddInt32(&m.conns, 1)

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
			if m.silentConnect {
				continue // сервер молчит: клиент обязан упасть по дедлайну
			}
			var p struct {
				Token string `json:"token"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.Token != "sock-token" {
				_ = conn.WriteJSON(map[string]any{"id": msg.ID,
					"error": map[string]any{"code": 1000, "message": "bad token"}})
				return
			}
			_ = conn.WriteJSON(map[string]any{"id": msg.ID,
				"result": map[string]any{"client": "uuid-1", "version": "2.2.1"}})
		case 1: // subscribe
			if m.silentSubscribe {
				continue // сервер молчит: клиент обязан упасть по дедлайну
			}
			var p struct {
				Channel string `json:"channel"`
				Token   string `json:"token"`
			}
			_ = json.Unmarshal(msg.Params, &p)
			if p.Channel != "$alerts:donation_42" || p.Token != "chan-token" {
				_ = conn.WriteJSON(map[string]any{"id": msg.ID,
					"error": map[string]any{"code": 1000, "message": "bad channel token"}})
				return
			}
			if err := conn.WriteJSON(map[string]any{"id": msg.ID,
				"result": map[string]any{"type": 1, "channel": p.Channel}}); err != nil {
				return
			}
			m.publish(conn, p.Channel, m.donationsFor(n))
			m.subOnce.Do(func() { close(m.subscribed) })
			if m.dropFirst && n == 1 {
				time.Sleep(50 * time.Millisecond) // публикация должна уйти клиенту
				return                            // имитация обрыва соединения
			}
		}
	}
}

// publish — публикации донатов: данные вложены в data.data — так отдаёт настоящий DA.
func (m *daEventsMock) publish(conn *websocket.Conn, channel string, donations []daMockDonation) {
	for _, d := range donations {
		_ = conn.WriteJSON(map[string]any{
			"method": 1,
			"params": map[string]any{
				"channel": channel,
				"data": map[string]any{
					"type": "donation",
					"data": map[string]any{
						"id":       d.ID,
						"username": d.Username,
						"message":  d.Message,
						"amount":   d.Amount,
						"currency": d.Currency,
					},
				},
			},
		})
	}
}

// daEventsStore — хранилище секретов в памяти (тестовая замена OS credential store).
type daEventsStore struct {
	mu     sync.Mutex
	values map[string][]byte
}

func newDAEventsStore() *daEventsStore {
	return &daEventsStore{values: map[string][]byte{}}
}

func (s *daEventsStore) Load(key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (s *daEventsStore) Save(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *daEventsStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

// daEventsProvider — провайдер с подменёнными эндпоинтами и сохранённой сессией.
func daEventsProvider(t *testing.T, m *daEventsMock) *DonationAlertsProvider {
	t.Helper()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "cid",
		ClientSecret: "csecret",
		Store:        newDAEventsStore(),
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
		UserURL:      m.srv.URL + "/api/v1/user/oauth",
		SubscribeURL: m.srv.URL + "/api/v1/centrifuge/subscribe",
		WSURL:        m.wsURL(),
	})

	session := DonationAlertsSession{
		AccessToken:  "acc-token",
		RefreshToken: "refresh-token",
		ExpiresAt:    time.Now().Add(time.Hour),
		UserID:       42,
	}
	if err := p.SaveSession(context.Background(), session); err != nil {
		t.Fatalf("сохранение сессии: %v", err)
	}

	return p
}

// daEventsRunner — запущенный поток событий: StartEvents в горутине, остановка по требованию.
type daEventsRunner struct {
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

// daStartEvents — запускает поток событий провайдера.
func daStartEvents(t *testing.T, p *DonationAlertsProvider) *daEventsRunner {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &daEventsRunner{cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- p.StartEvents(ctx) }()
	t.Cleanup(func() { r.stop(t) })
	return r
}

// stop — отменяет поток и ждёт завершения: StartEvents после отмены возвращает nil.
func (r *daEventsRunner) stop(t *testing.T) {
	t.Helper()
	r.once.Do(func() {
		r.cancel()
		select {
		case err := <-r.done:
			if err != nil {
				t.Errorf("StartEvents после отмены: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("StartEvents не завершился за 5 с")
		}
	})
}

// wait — ждёт завершения потока и возвращает ошибку StartEvents.
func (r *daEventsRunner) wait(t *testing.T) error {
	t.Helper()
	var err error
	r.once.Do(func() {
		select {
		case err = <-r.done:
		case <-time.After(5 * time.Second):
			t.Error("StartEvents не завершился за 5 с")
		}
	})
	return err
}

// daWaitRunning — ждёт, пока поток событий поднимется.
func daWaitRunning(t *testing.T, p *DonationAlertsProvider) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p.EventsRunning() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("поток событий не поднялся за 2 с")
}

// TestDonationAlertsEventsDonation — донат из публикации доходит до обработчика через On.
func TestDonationAlertsEventsDonation(t *testing.T) {
	m := newDAEventsMock(t, []daMockDonation{
		{ID: 5, Username: "Ivan", Message: "hi", Amount: 100.5, Currency: "RUB"},
	})
	p := daEventsProvider(t, m)

	events := make(chan DonationEvent, 8)
	p.On(func(eventType EventType, data interface{}) {
		if eventType != EventTypeDonation {
			return
		}
		ev, ok := data.(DonationEvent)
		if !ok {
			return
		}
		select {
		case events <- ev:
		default:
		}
	})

	daStartEvents(t, p)
	daWaitRunning(t, p)

	select {
	case ev := <-events:
		if ev.ID != 5 || ev.Username != "Ivan" || ev.Message != "hi" ||
			ev.Amount != 100.5 || ev.Currency != "RUB" {
			t.Fatalf("донат %+v, want {5 Ivan hi 100.5 RUB}", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("донат не пришёл за 5 с")
	}

	// REST-часть потока: профиль и подписка с client id из connect-ответа.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.userAuth != "Bearer acc-token" {
		t.Errorf("Authorization профиля = %q, want Bearer acc-token", m.userAuth)
	}
	if m.restAuth != "Bearer acc-token" {
		t.Errorf("Authorization подписки = %q, want Bearer acc-token", m.restAuth)
	}
	if len(m.restBody.Channels) != 1 || m.restBody.Channels[0] != "$alerts:donation_42" {
		t.Errorf("channels подписки = %v, want [$alerts:donation_42]", m.restBody.Channels)
	}
	if m.restBody.Client != "uuid-1" {
		t.Errorf("client подписки = %q, want uuid-1 (из WS-connect)", m.restBody.Client)
	}
}

// TestDonationAlertsEventsDeduplicate — повтор той же публикации не доходит до обработчика второй раз.
func TestDonationAlertsEventsDeduplicate(t *testing.T) {
	m := newDAEventsMock(t, []daMockDonation{
		{ID: 7, Username: "Ivan", Amount: 500, Currency: "RUB"},
		{ID: 7, Username: "Ivan", Amount: 500, Currency: "RUB"},
	})
	p := daEventsProvider(t, m)

	var count int32
	events := make(chan DonationEvent, 8)
	p.On(func(eventType EventType, data interface{}) {
		if eventType != EventTypeDonation {
			return
		}
		ev, ok := data.(DonationEvent)
		if !ok {
			return
		}
		atomic.AddInt32(&count, 1)
		select {
		case events <- ev:
		default:
		}
	})

	daStartEvents(t, p)

	select {
	case ev := <-events:
		if ev.ID != 7 || ev.Amount != 500 || ev.Currency != "RUB" {
			t.Fatalf("донат %+v, want {7 Ivan 500 RUB}", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("донат не пришёл за 5 с")
	}

	// Второй такой же донат должен быть отсечён по id.
	time.Sleep(500 * time.Millisecond)
	if got := atomic.LoadInt32(&count); got != 1 {
		t.Fatalf("обработчик вызван %d раз, want 1 (повтор отсечён по id)", got)
	}
}

// TestDonationAlertsEventsStop — StopEvents останавливает поток, StartEvents возвращает nil.
func TestDonationAlertsEventsStop(t *testing.T) {
	m := newDAEventsMock(t, nil)
	p := daEventsProvider(t, m)

	runner := daStartEvents(t, p)
	daWaitRunning(t, p)

	p.StopEvents()
	if err := runner.wait(t); err != nil {
		t.Fatalf("StartEvents после StopEvents: %v", err)
	}
	if p.EventsRunning() {
		t.Error("EventsRunning = true после остановки потока")
	}
}

// TestDonationAlertsEventsWithoutSession — без входа поток событий не начинается.
func TestDonationAlertsEventsWithoutSession(t *testing.T) {
	m := newDAEventsMock(t, nil)
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "cid",
		ClientSecret: "csecret",
		Store:        newDAEventsStore(),
		HTTPClient:   &http.Client{Timeout: 5 * time.Second},
		UserURL:      m.srv.URL + "/api/v1/user/oauth",
		SubscribeURL: m.srv.URL + "/api/v1/centrifuge/subscribe",
		WSURL:        m.wsURL(),
	})

	if err := p.StartEvents(context.Background()); err == nil {
		t.Fatal("StartEvents без сессии вернул nil, want ошибку")
	}
	if p.EventsRunning() {
		t.Error("EventsRunning = true после неудачного старта")
	}
}

// TestDonationAlertsEventsReconnect — после обрыва соединения провайдер подключается заново.
func TestDonationAlertsEventsReconnect(t *testing.T) {
	m := newDAEventsMock(t,
		[]daMockDonation{{ID: 1, Username: "Ivan", Amount: 100, Currency: "RUB"}},
		[]daMockDonation{{ID: 2, Username: "Petr", Amount: 200, Currency: "RUB"}},
	)
	m.dropFirst = true
	p := daEventsProvider(t, m)

	// Паузу между попытками уменьшаем: тест не должен ждать реальные 5 с.
	st := p.daEventsStateFor()
	st.mu.Lock()
	st.minBackoff = 10 * time.Millisecond
	st.maxBackoff = 20 * time.Millisecond
	st.mu.Unlock()

	events := make(chan DonationEvent, 8)
	p.On(func(eventType EventType, data interface{}) {
		if eventType != EventTypeDonation {
			return
		}
		ev, ok := data.(DonationEvent)
		if !ok {
			return
		}
		select {
		case events <- ev:
		default:
		}
	})

	daStartEvents(t, p)

	select {
	case ev := <-events:
		if ev.ID != 1 {
			t.Fatalf("первый донат %+v, want ID 1", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("первый донат не пришёл за 5 с")
	}

	select {
	case ev := <-events:
		if ev.ID != 2 {
			t.Fatalf("донат после реконнекта %+v, want ID 2", ev)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("донат после реконнекта не пришёл за 10 с")
	}
}

// daEventsFastState — состояние потока с короткими таймаутами: тест не должен ждать реальные 15 с.
func daEventsFastState(t *testing.T, p *DonationAlertsProvider) *daEventsState {
	t.Helper()
	st := p.daEventsStateFor()
	st.mu.Lock()
	st.requestTimeout = 200 * time.Millisecond
	st.minBackoff = 10 * time.Millisecond
	st.maxBackoff = 20 * time.Millisecond
	st.mu.Unlock()
	return st
}

// TestDonationAlertsEventsRequestTimeout — сервер, принявший соединение, но не ответивший, не вешает поток:
// и connect, и subscribe ждут ответа не дольше дедлайна.
func TestDonationAlertsEventsRequestTimeout(t *testing.T) {
	for _, tc := range []struct {
		name            string
		silentConnect   bool
		silentSubscribe bool
	}{
		{name: "нет ответа на connect", silentConnect: true},
		{name: "нет ответа на subscribe", silentSubscribe: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newDAEventsMock(t, nil)
			m.silentConnect = tc.silentConnect
			m.silentSubscribe = tc.silentSubscribe
			p := daEventsProvider(t, m)
			st := daEventsFastState(t, p)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			start := time.Now()
			established, err := p.daEventsConnect(ctx, st)
			elapsed := time.Since(start)

			if err == nil {
				t.Fatal("daEventsConnect: want ошибка, got nil")
			}
			if established {
				t.Error("established = true, want false")
			}
			if elapsed > 3*time.Second {
				t.Fatalf("попытка заняла %v, want меньше 3 с", elapsed)
			}
		})
	}
}

// TestDonationAlertsEventsReconnectAfterTimeout — молчащий сервер не залипает: поток идёт на следующую попытку.
func TestDonationAlertsEventsReconnectAfterTimeout(t *testing.T) {
	m := newDAEventsMock(t, nil)
	m.silentConnect = true
	p := daEventsProvider(t, m)
	daEventsFastState(t, p)

	daStartEvents(t, p)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&m.conns) >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("попыток подключения = %d, want >= 2 (поток не завис)", atomic.LoadInt32(&m.conns))
}

// TestDonationAlertsEventsContextCancelDuringRequest — отмена контекста прерывает ожидание ответа Centrifugo
// и даёт штатную остановку (nil), а не ошибку.
func TestDonationAlertsEventsContextCancelDuringRequest(t *testing.T) {
	m := newDAEventsMock(t, nil)
	m.silentConnect = true
	p := daEventsProvider(t, m)
	st := daEventsFastState(t, p)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.daEventsConnect(ctx, st)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("daEventsConnect после отмены: %v, want nil (штатная остановка)", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daEventsConnect не вернулся после отмены контекста")
	}
}

// TestDonationAlertsSubscribeRESTChannelToken — токен берётся только у запрошенного канала.
func TestDonationAlertsSubscribeRESTChannelToken(t *testing.T) {
	for _, tc := range []struct {
		name     string
		channels []map[string]string
		wantErr  bool
	}{
		{
			name:     "только чужой канал",
			channels: []map[string]string{{"channel": "$alerts:donation_99", "token": "other-token"}},
			wantErr:  true,
		},
		{
			name:     "нужный канал с пустым токеном",
			channels: []map[string]string{{"channel": "$alerts:donation_42", "token": ""}},
			wantErr:  true,
		},
		{
			name: "нужный канал среди чужих",
			channels: []map[string]string{
				{"channel": "$alerts:donation_99", "token": "other-token"},
				{"channel": "$alerts:donation_42", "token": "chan-token"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newDAEventsMock(t, nil)
			m.restResponse = tc.channels
			p := daEventsProvider(t, m)

			token, err := p.daSubscribeREST(context.Background(),
				m.srv.URL+"/api/v1/centrifuge/subscribe", "acc-token", "$alerts:donation_42", "uuid-1")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("daSubscribeREST = (%q, nil), want ошибку", token)
				}
				return
			}
			if err != nil {
				t.Fatalf("daSubscribeREST: %v", err)
			}
			if token != "chan-token" {
				t.Errorf("token = %q, want chan-token", token)
			}
		})
	}
}

// TestDonationAlertsEventsStateCleanup — после остановки потока запись провайдера убрана из daEventsStates.
func TestDonationAlertsEventsStateCleanup(t *testing.T) {
	m := newDAEventsMock(t, nil)
	p := daEventsProvider(t, m)

	// StopEvents у провайдера без потока не должен оставлять запись.
	p.StopEvents()
	if _, ok := daEventsStates.Load(p); ok {
		t.Fatal("StopEvents без потока оставил запись в daEventsStates")
	}

	runner := daStartEvents(t, p)
	daWaitRunning(t, p)
	if _, ok := daEventsStates.Load(p); !ok {
		t.Fatal("во время работы потока записи провайдера нет в daEventsStates")
	}

	p.StopEvents()
	if err := runner.wait(t); err != nil {
		t.Fatalf("StartEvents после StopEvents: %v", err)
	}
	if _, ok := daEventsStates.Load(p); ok {
		t.Fatal("запись провайдера осталась в daEventsStates после остановки потока")
	}
	if p.EventsRunning() {
		t.Error("EventsRunning = true после остановки потока")
	}
}
