package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DonationEvent — донат из публикации канала $alerts:donation_<user_id>.
type DonationEvent struct {
	ID       int64   `json:"id"`
	Username string  `json:"username"`
	Message  string  `json:"message"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

const (
	// daEventsMinBackoff — пауза перед повторной попыткой подключения.
	daEventsMinBackoff = 5 * time.Second
	// daEventsMaxBackoff — потолок паузы между попытками.
	daEventsMaxBackoff = 30 * time.Second
	// daEventsSeenLimit — размер кольца id донатов (дедупликация).
	daEventsSeenLimit = 512
	// daEventsDialTimeout — таймаут WS-рукопожатия.
	daEventsDialTimeout = 15 * time.Second
	// daEventsRequestTimeout — дедлайн ожидания ответа Centrifugo (connect/subscribe).
	daEventsRequestTimeout = 15 * time.Second
)

// daEventsState — состояние потока событий DA; ключ в daEventsStates — сам провайдер.
type daEventsState struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	gen     uint64 // счётчик потоков: завершившийся не сбрасывает новый
	seen    *daSeenIDs

	// minBackoff/maxBackoff — пауза между попытками подключения; тесты уменьшают её.
	minBackoff time.Duration
	maxBackoff time.Duration
	// requestTimeout — дедлайн ответа Centrifugo; тесты уменьшают его.
	requestTimeout time.Duration
}

// daEventsStates — состояния потоков событий по провайдерам.
var daEventsStates sync.Map // *DonationAlertsProvider → *daEventsState

// daEventsStateFor — состояние потока событий данного провайдера; запись создаётся при первом обращении.
func (p *DonationAlertsProvider) daEventsStateFor() *daEventsState {
	if v, ok := daEventsStates.Load(p); ok {
		return v.(*daEventsState)
	}
	st := &daEventsState{
		seen:           newDaSeenIDs(daEventsSeenLimit),
		minBackoff:     daEventsMinBackoff,
		maxBackoff:     daEventsMaxBackoff,
		requestTimeout: daEventsRequestTimeout,
	}
	v, _ := daEventsStates.LoadOrStore(p, st)
	return v.(*daEventsState)
}

// daEventsState — состояние потока провайдера без создания записи (nil — поток не запускался).
func (p *DonationAlertsProvider) daEventsState() *daEventsState {
	v, ok := daEventsStates.Load(p)
	if !ok {
		return nil
	}
	return v.(*daEventsState)
}

// daEventsRun — зарегистрированный поток событий.
type daEventsRun struct {
	ctx    context.Context
	cancel context.CancelFunc
	st     *daEventsState
	gen    uint64
	prev   context.CancelFunc // отмена прежнего потока
}

// daEventsRegister — регистрирует поток событий: готовит состояние и отдаёт отмену прежнего потока.
func (p *DonationAlertsProvider) daEventsRegister(ctx context.Context) daEventsRun {
	for {
		st := p.daEventsStateFor()
		st.mu.Lock()
		// Пока ждали мьютекс, запись могли убрать (поток завершился) — состояние берём заново.
		if cur, ok := daEventsStates.Load(p); !ok || cur != st {
			st.mu.Unlock()
			continue
		}
		prev := st.cancel
		st.gen++
		gen := st.gen
		eventsCtx, cancel := context.WithCancel(ctx)
		st.cancel = cancel
		st.running = true
		st.mu.Unlock()
		return daEventsRun{ctx: eventsCtx, cancel: cancel, st: st, gen: gen, prev: prev}
	}
}

// daEventsUnregister — поток завершён: снимаем состояние и убираем запись провайдера, чтобы она не текла.
func (p *DonationAlertsProvider) daEventsUnregister(st *daEventsState, gen uint64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.gen != gen {
		return // место потока занял новый
	}
	st.running = false
	st.cancel = nil
	daEventsStates.CompareAndDelete(p, st)
}

// daSeenIDs — кольцо последних id донатов: реконнект может переиграть публикацию.
type daSeenIDs struct {
	mu  sync.Mutex
	ids []int64
	pos int
}

// newDaSeenIDs — кольцо на limit последних id.
func newDaSeenIDs(limit int) *daSeenIDs {
	if limit <= 0 {
		limit = 1
	}
	return &daSeenIDs{ids: make([]int64, 0, limit)}
}

// seen — был ли id уже обработан (и запоминает его, если нет). Донат без id (0) не дедуплицируется.
func (s *daSeenIDs) seen(id int64) bool {
	if id == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.ids {
		if v == id {
			return true
		}
	}
	if len(s.ids) < cap(s.ids) {
		s.ids = append(s.ids, id)
		return false
	}
	s.ids[s.pos] = id
	s.pos = (s.pos + 1) % cap(s.ids)
	return false
}

// StartEvents — запускает поток событий DonationAlerts и блокируется до отмены ctx. При обрыве
// соединения переподключается сам: пауза растёт от 5 с до 30 с и сбрасывается после удачного
// подключения. Повторный вызов останавливает прежний поток: у провайдера он один.
// Возвращает nil при отмене, ошибку — только когда события начать нельзя (нет сессии).
func (p *DonationAlertsProvider) StartEvents(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}

	run := p.daEventsRegister(ctx)
	if run.prev != nil {
		run.prev() // прежний поток должен уйти
	}

	defer func() {
		run.cancel()
		p.daEventsUnregister(run.st, run.gen)
	}()

	// Сессия нужна до первой попытки: без входа события не поедут, и повторять попытки бессмысленно.
	if _, err := p.daEventsSession(ctx); err != nil {
		return err
	}

	backoff := run.st.minBackoff
	for {
		if run.ctx.Err() != nil {
			return nil
		}

		established, err := p.daEventsConnect(run.ctx, run.st)
		if run.ctx.Err() != nil {
			return nil // штатная остановка
		}
		if err != nil {
			fmt.Println("donationalerts: поток событий:", err)
		}
		if established {
			// Соединение было живым — паузу начинаем сначала.
			backoff = run.st.minBackoff
		}

		timer := time.NewTimer(backoff)
		select {
		case <-run.ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if backoff < run.st.maxBackoff {
			backoff *= 2
			if backoff > run.st.maxBackoff {
				backoff = run.st.maxBackoff
			}
		}
	}
}

// StopEvents — останавливает поток событий: отменяет его контекст. Поток завершается сам.
func (p *DonationAlertsProvider) StopEvents() {
	st := p.daEventsState()
	if st == nil {
		return
	}
	st.mu.Lock()
	cancel := st.cancel
	st.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// EventsRunning — идёт ли поток событий. IsRunning про вход.
func (p *DonationAlertsProvider) EventsRunning() bool {
	st := p.daEventsState()
	if st == nil {
		return false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.running
}

// daEventsConnect — одна попытка: профиль → WS-connect → REST-подписка → WS-подписка → чтение публикаций.
// established=true, если подписка подтверждена.
func (p *DonationAlertsProvider) daEventsConnect(ctx context.Context, st *daEventsState) (bool, error) {
	session, err := p.daEventsSession(ctx)
	if err != nil {
		return false, err
	}

	_, userURL, subscribeURL, wsURL := p.Endpoints()
	if userURL == "" {
		userURL = DonationAlertsUserURL
	}
	if subscribeURL == "" {
		subscribeURL = DonationAlertsSubscribeURL
	}
	if wsURL == "" {
		wsURL = DonationAlertsWSURL
	}

	profile, err := p.daFetchProfile(ctx, userURL, session.AccessToken)
	if err != nil {
		return false, err
	}
	if profile.SocketConnectionToken == "" {
		return false, errors.New("donationalerts: в профиле нет socket_connection_token (нужен скоуп oauth-user-show)")
	}
	if profile.ID == 0 {
		return false, errors.New("donationalerts: в профиле нет id пользователя")
	}

	channel := fmt.Sprintf("$alerts:donation_%d", profile.ID)

	conn, err := daEventsDial(ctx, wsURL)
	if err != nil {
		return false, err
	}
	defer conn.Close()

	// Сторож: отмена контекста срывает блокирующее чтение. Ставим до запросов — иначе зависнет ожидание ответа.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetReadDeadline(time.Now())
		case <-stop:
		}
	}()

	// Публикации, пришедшие пока ждём ответа, не теряем: их разбирает daEventsHandle.
	onPublication := func(raw []byte) {
		p.daEventsHandle(raw, channel, st)
	}

	clientID, err := daEventsRequest(ctx, conn, st.requestTimeout, 0, 1, map[string]any{
		"token":   profile.SocketConnectionToken,
		"name":    "timer-widget",
		"version": "1.0",
	}, onPublication)
	if err != nil {
		if ctx.Err() != nil {
			return true, nil // штатная остановка
		}
		return false, fmt.Errorf("donationalerts: centrifugo connect: %w", err)
	}

	channelToken, err := p.daSubscribeREST(ctx, subscribeURL, session.AccessToken, channel, clientID)
	if err != nil {
		return false, err
	}

	if _, err := daEventsRequest(ctx, conn, st.requestTimeout, 1, 2, map[string]any{
		"channel": channel,
		"token":   channelToken,
	}, onPublication); err != nil {
		if ctx.Err() != nil {
			return true, nil // штатная остановка
		}
		return false, fmt.Errorf("donationalerts: centrifugo subscribe: %w", err)
	}

	// Публикации ждём без дедлайна: отмена срывает чтение сторожем.
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return false, fmt.Errorf("donationalerts: centrifugo: сброс дедлайна: %w", err)
	}
	if ctx.Err() != nil {
		return true, nil // штатная остановка
	}

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return true, nil // штатная остановка
			}
			return true, fmt.Errorf("donationalerts: centrifugo чтение: %w", err)
		}
		p.daEventsHandle(raw, channel, st)
	}
}

// daEventsHandle — разбор публикации доната. Формат: params.data = {"type":"donation","data":{id,username,message,amount,currency}}.
func (p *DonationAlertsProvider) daEventsHandle(raw []byte, channel string, st *daEventsState) {
	var msg struct {
		Method int             `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Method != 1 {
		return // служебные сообщения и пинги
	}

	var pub struct {
		Channel string `json:"channel"`
		Data    struct {
			Type string            `json:"type"`
			Data daDonationPayload `json:"data"`
		} `json:"data"`
	}
	if json.Unmarshal(msg.Params, &pub) != nil {
		return
	}
	if pub.Channel != "" && pub.Channel != channel {
		return // публикация из другого канала
	}
	if pub.Data.Type != "" && pub.Data.Type != "donation" {
		return // не донат
	}

	event, ok := pub.Data.Data.event()
	if !ok {
		return
	}
	if st.seen.seen(event.ID) {
		return // повтор: реконнект переиграл публикацию
	}

	p.emit(EventTypeDonation, event)
}

// daEventsSession — сессия для потока событий: из памяти, иначе из хранилища; протухший access-токен обновляется.
func (p *DonationAlertsProvider) daEventsSession(ctx context.Context) (DonationAlertsSession, error) {
	session, ok := p.Session()
	if !ok {
		loaded, found, err := p.LoadSession(ctx)
		if err != nil {
			return DonationAlertsSession{}, fmt.Errorf("donationalerts: загрузка сессии: %w", err)
		}
		if !found {
			return DonationAlertsSession{}, errors.New("donationalerts: нет сессии — войдите в DonationAlerts")
		}
		session = loaded
	}
	if session.AccessToken == "" {
		return DonationAlertsSession{}, errors.New("donationalerts: в сессии нет access-токена")
	}

	if !session.ExpiresAt.IsZero() && !time.Now().Before(session.ExpiresAt) {
		refreshed, err := p.RefreshSession(ctx)
		if err != nil {
			return DonationAlertsSession{}, fmt.Errorf("donationalerts: обновление сессии: %w", err)
		}
		if refreshed.AccessToken == "" {
			return DonationAlertsSession{}, errors.New("donationalerts: обновление сессии вернуло пустой access-токен")
		}
		// Ошибка сохранения не рвёт поток: токен уже обновлён в памяти.
		if err := p.SaveSession(ctx, refreshed); err != nil {
			fmt.Println("donationalerts: сохранение обновлённой сессии:", err)
		}
		session = refreshed
	}

	return session, nil
}

// daEventsProfile — профиль DA: id пользователя (из него собирается канал $alerts:donation_<id>) и токен Centrifugo.
type daEventsProfile struct {
	ID                    int64
	Code                  string
	SocketConnectionToken string
}

// daFetchProfile — GET профиля DA. Ответ обёрнут в {"data":{…}}: нужны data.id и data.socket_connection_token.
func (p *DonationAlertsProvider) daFetchProfile(ctx context.Context, userURL, accessToken string) (daEventsProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userURL, nil)
	if err != nil {
		return daEventsProfile{}, fmt.Errorf("donationalerts: профиль: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.daHTTPClient().Do(req)
	if err != nil {
		return daEventsProfile{}, fmt.Errorf("donationalerts: профиль: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return daEventsProfile{}, fmt.Errorf("donationalerts: профиль: чтение ответа: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return daEventsProfile{}, fmt.Errorf("donationalerts: профиль (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var r struct {
		Data struct {
			ID                    int64  `json:"id"`
			Code                  string `json:"code"`
			SocketConnectionToken string `json:"socket_connection_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return daEventsProfile{}, fmt.Errorf("donationalerts: профиль: разбор ответа: %w", err)
	}

	return daEventsProfile{
		ID:                    r.Data.ID,
		Code:                  r.Data.Code,
		SocketConnectionToken: r.Data.SocketConnectionToken,
	}, nil
}

// daSubscribeREST — POST /api/v1/centrifuge/subscribe: возвращает токен канала для client-side подписки по WS.
func (p *DonationAlertsProvider) daSubscribeREST(ctx context.Context, subscribeURL, accessToken, channel, clientID string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"channels": []string{channel},
		"client":   clientID,
	})
	if err != nil {
		return "", fmt.Errorf("donationalerts: подписка: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, subscribeURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("donationalerts: подписка: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := p.daHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("donationalerts: подписка: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("donationalerts: подписка: чтение ответа: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("donationalerts: подписка (%d): %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var r struct {
		Channels []struct {
			Channel string `json:"channel"`
			Token   string `json:"token"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", fmt.Errorf("donationalerts: подписка: разбор ответа: %w", err)
	}

	var token string
	found := false
	for _, ch := range r.Channels {
		if ch.Channel != channel {
			continue
		}
		found = true
		token = ch.Token
		break
	}
	if !found {
		return "", fmt.Errorf("donationalerts: подписка: в ответе нет канала %s", channel)
	}
	if token == "" {
		return "", fmt.Errorf("donationalerts: подписка: сервер не вернул токен канала %s", channel)
	}

	return token, nil
}

// daHTTPClient — HTTP-клиент провайдера (nil → стандартный).
func (p *DonationAlertsProvider) daHTTPClient() *http.Client {
	if c := p.HTTPClient(); c != nil {
		return c
	}
	return http.DefaultClient
}

// daEventsDial — WS-подключение к Centrifugo.
func daEventsDial(ctx context.Context, wsURL string) (*websocket.Conn, error) {
	dialer := websocket.Dialer{HandshakeTimeout: daEventsDialTimeout}
	conn, resp, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("donationalerts: centrifugo (%d): %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("donationalerts: centrifugo: %w", err)
	}
	return conn, nil
}

// daEventsRequest — отправляет клиентское сообщение Centrifugo и ждёт ответа с тем же id, пропуская серверные
// пуши: публикации при этом не теряются — их разбирает onPublication. Для connect (method 0) возвращает client id.
// Ответ ждём не дольше timeout: сервер мог принять соединение, но не ответить — иначе поток зависнет навсегда.
func daEventsRequest(ctx context.Context, conn *websocket.Conn, timeout time.Duration, method, id int, params map[string]any, onPublication func(raw []byte)) (string, error) {
	req, err := json.Marshal(map[string]any{"method": method, "id": id, "params": params})
	if err != nil {
		return "", fmt.Errorf("donationalerts: centrifugo: %w", err)
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return "", fmt.Errorf("donationalerts: centrifugo: дедлайн записи: %w", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, req); err != nil {
		return "", fmt.Errorf("donationalerts: centrifugo: запись: %w", err)
	}

	// Дедлайн ожидания ответа.
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", fmt.Errorf("donationalerts: centrifugo: дедлайн ответа: %w", err)
	}
	if ctx.Err() != nil {
		_ = conn.SetReadDeadline(time.Now())
		return "", ctx.Err()
	}

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if daEventsTimeout(err) {
				return "", fmt.Errorf("donationalerts: centrifugo: ожидание ответа (method %d) истекло: %w", method, err)
			}
			return "", err
		}

		var msg struct {
			ID     int             `json:"id"`
			Method int             `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			continue // мусор в кадре — ждём дальше
		}
		if msg.ID != id {
			// Пуш сервера: публикацию разобрать надо, а не потерять.
			if msg.Method != 0 && onPublication != nil {
				onPublication(raw)
			}
			continue
		}
		if msg.Error != nil {
			return "", fmt.Errorf("donationalerts: centrifugo: код %d: %s", msg.Error.Code, msg.Error.Message)
		}

		if method == 0 {
			var res struct {
				Client string `json:"client"`
			}
			if err := json.Unmarshal(msg.Result, &res); err != nil {
				return "", fmt.Errorf("donationalerts: centrifugo: результат connect: %w", err)
			}
			if res.Client == "" {
				return "", errors.New("donationalerts: centrifugo: в ответе connect нет client id")
			}
			return res.Client, nil
		}

		return "", nil
	}
}

// daEventsTimeout — истёк ли дедлайн ожидания.
func daEventsTimeout(err error) bool {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// daDonationPayload — «сырая» публикация доната: id и amount DA присылает то числом, то строкой.
type daDonationPayload struct {
	ID       json.RawMessage `json:"id"`
	Username string          `json:"username"`
	Message  json.RawMessage `json:"message"`
	Amount   json.RawMessage `json:"amount"`
	Currency string          `json:"currency"`
}

// event — донат из публикации. ok=false — в публикации нет данных доната.
func (d daDonationPayload) event() (DonationEvent, bool) {
	event := DonationEvent{
		ID:       daJSONInt64(d.ID),
		Username: d.Username,
		Message:  daJSONString(d.Message),
		Amount:   daJSONFloat(d.Amount),
		Currency: d.Currency,
	}
	if event.ID == 0 && event.Amount == 0 {
		return DonationEvent{}, false
	}
	return event, true
}

// daJSONInt64 — целое из JSON-числа или JSON-строки с числом.
func daJSONInt64(raw json.RawMessage) int64 {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0
	}
	if v, err := strconv.ParseInt(text, 10, 64); err == nil {
		return v
	}
	if v, err := strconv.ParseFloat(text, 64); err == nil {
		return int64(v)
	}
	return 0
}

// daJSONFloat — число из JSON-числа или JSON-строки с числом.
func daJSONFloat(raw json.RawMessage) float64 {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return 0
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0
	}
	return v
}

// daJSONString — строка из JSON-строки (не строка — пусто).
func daJSONString(raw json.RawMessage) string {
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "null" {
		return ""
	}
	if !strings.HasPrefix(text, `"`) {
		return ""
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}
	return v
}
