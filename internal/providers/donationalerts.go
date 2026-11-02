package providers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	ProviderNameDonationAlerts ProviderName = "DonationAlerts"

	// DonationAlertsSessionSecretKey — ключ сессии в OS credential store.
	DonationAlertsSessionSecretKey = "donationalerts.session"

	DonationAlertsAuthorizeURL = "https://www.donationalerts.com/oauth/authorize"
	DonationAlertsTokenURL     = "https://www.donationalerts.com/oauth/token"
	DonationAlertsUserURL      = "https://www.donationalerts.com/api/v1/user/oauth"
	DonationAlertsSubscribeURL = "https://www.donationalerts.com/api/v1/centrifuge/subscribe"
	DonationAlertsWSURL        = "wss://centrifugo.donationalerts.com/connection/websocket"

	// DonationAlertsScope — скоупы DA, через пробел.
	DonationAlertsScope = "oauth-user-show oauth-donation-subscribe"

	// Порт и путь зафиксированы: DA сверяет redirect_uri с зарегистрированным
	// в приложении, менять их нельзя.
	DonationAlertsRedirectPort = 13579
	DonationAlertsRedirectPath = "/callback"
)

var (
	// daDefaultListenAddr — loopback-адрес провайдера по умолчанию.
	daDefaultListenAddr = fmt.Sprintf("127.0.0.1:%d", DonationAlertsRedirectPort)
	// daDefaultRedirectURL — redirect_uri по умолчанию; хост — localhost, так
	// он зарегистрирован в приложении DonationAlerts.
	daDefaultRedirectURL = fmt.Sprintf("http://localhost:%d%s", DonationAlertsRedirectPort, DonationAlertsRedirectPath)
)

const (
	// daDefaultTokenTTL — срок жизни access-токена, если DA не прислал expires_in.
	daDefaultTokenTTL = 24 * time.Hour
	// daStateBytes — длина state в байтах (до кодирования base64url).
	daStateBytes = 32
)

// Страницы, которые браузер показывает после callback.
const (
	daSuccessPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>Готово</title></head>
<body><h2>Авторизация успешна</h2><p>Можете закрыть эту вкладку и вернуться в приложение.</p></body></html>`

	daErrorPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>Ошибка</title></head>
<body><h2>Авторизация не выполнена</h2><p>Вернитесь в приложение и попробуйте ещё раз.</p></body></html>`
)

// DonationAlertsSession — вход DonationAlerts; хранится в OS credential store.
type DonationAlertsSession struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	UserID       int64     `json:"user_id,omitempty"`
	UserCode     string    `json:"user_code,omitempty"` // «code» из профиля DA
}

// DonationAlertsAuth — начало входа: ссылка для браузера, redirect и state.
type DonationAlertsAuth struct {
	AuthorizeURL string
	RedirectURL  string
	State        string
}

// DonationAlertsOptions — параметры провайдера; URL'ы и ListenAddr нужны тестам.
type DonationAlertsOptions struct {
	ClientID     string
	ClientSecret string
	Store        SecretStore
	HTTPClient   *http.Client
	AuthorizeURL string // пусто → DonationAlertsAuthorizeURL
	TokenURL     string // пусто → DonationAlertsTokenURL
	UserURL      string // пусто → DonationAlertsUserURL
	SubscribeURL string // пусто → DonationAlertsSubscribeURL
	WSURL        string // пусто → DonationAlertsWSURL
	RedirectURL  string // пусто → http://localhost:13579/callback
	ListenAddr   string // пусто → 127.0.0.1:13579
}

// daProfile — профиль DA из GET /api/v1/user/oauth; ответ обёрнут в {"data":{…}}.
type daProfile struct {
	ID                    int64  `json:"id"`
	Code                  string `json:"code"`
	SocketConnectionToken string `json:"socket_connection_token"`
}

// daCallback — результат callback'а браузера.
type daCallback struct {
	code  string
	state string
	err   error
}

// daPendingAuth — состояние текущего входа: loopback-сервер и канал с результатом.
type daPendingAuth struct {
	state    string
	redirect string
	resultCh chan daCallback
	ctx      context.Context
	cancel   context.CancelCauseFunc
	srv      *http.Server
	// ln — основной слушатель (IPv4), ln6 — дополнительный ([::1]). Держим их,
	// чтобы закрыть явно: http.Server.Close() закрывает только обслуживаемые.
	ln  net.Listener
	ln6 net.Listener
}

// send кладёт результат в канал, не блокируя обработчик HTTP: повторный callback не должен вешать сервер.
func (a *daPendingAuth) send(cb daCallback) {
	select {
	case a.resultCh <- cb:
	default:
	}
}

var _ Provider = (*DonationAlertsProvider)(nil)

// DonationAlertsProvider — провайдер DonationAlerts: OAuth authorization code с client_secret (DA не поддерживает PKCE).
type DonationAlertsProvider struct {
	clientID     string
	clientSecret string
	store        SecretStore
	http         *http.Client

	authorizeURL string
	tokenURL     string
	userURL      string
	subscribeURL string
	wsURL        string
	redirectURL  string
	listenAddr   string

	// storeMu сериализует обращения к OS credential store: держать p.mu во
	// время вызовов хранилища нельзя (его читают обработчики на frame thread).
	storeMu sync.Mutex
	mu      sync.Mutex

	// pending — идёт ли вход; auth — его состояние; cancelAuth — отмена ожидания.
	pending    bool
	auth       *daPendingAuth
	cancelAuth context.CancelCauseFunc

	session    DonationAlertsSession
	hasSession bool
	// sessionEpoch — счётчик изменений сессии: LogoutAt сверяет снимок.
	sessionEpoch uint64

	// socketToken — socket_connection_token из профиля DA; в сессии его нет, но подписке на донаты он нужен.
	socketToken string

	handler EventHandler
}

// NewDonationAlertsProvider подставляет дефолты эндпоинтов, адреса loopback-сервера и HTTP-клиента.
func NewDonationAlertsProvider(opts DonationAlertsOptions) *DonationAlertsProvider {
	p := &DonationAlertsProvider{
		clientID:     strings.TrimSpace(opts.ClientID),
		clientSecret: strings.TrimSpace(opts.ClientSecret),
		store:        opts.Store,
		http:         opts.HTTPClient,
		authorizeURL: opts.AuthorizeURL,
		tokenURL:     opts.TokenURL,
		userURL:      opts.UserURL,
		subscribeURL: opts.SubscribeURL,
		wsURL:        opts.WSURL,
		redirectURL:  opts.RedirectURL,
		listenAddr:   opts.ListenAddr,
	}
	if p.http == nil {
		p.http = &http.Client{Timeout: 30 * time.Second}
	}
	if p.authorizeURL == "" {
		p.authorizeURL = DonationAlertsAuthorizeURL
	}
	if p.tokenURL == "" {
		p.tokenURL = DonationAlertsTokenURL
	}
	if p.userURL == "" {
		p.userURL = DonationAlertsUserURL
	}
	if p.subscribeURL == "" {
		p.subscribeURL = DonationAlertsSubscribeURL
	}
	if p.wsURL == "" {
		p.wsURL = DonationAlertsWSURL
	}
	if p.redirectURL == "" {
		p.redirectURL = daDefaultRedirectURL
	}
	if p.listenAddr == "" {
		p.listenAddr = daDefaultListenAddr
	}
	return p
}

func (p *DonationAlertsProvider) GetName() ProviderName { return ProviderNameDonationAlerts }

// On сохраняет подписчика событий.
func (p *DonationAlertsProvider) On(handler EventHandler) {
	p.mu.Lock()
	p.handler = handler
	p.mu.Unlock()
}

// IsRunning — идёт ли вход (loopback-сервер поднят, ждём callback).
func (p *DonationAlertsProvider) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pending
}

// Enable запрещён: без UI ссылку входа никто не увидит, UI вызывает BeginAuth/WaitAuth.
func (p *DonationAlertsProvider) Enable(ctx context.Context) (string, error) {
	return "", errors.New("donationalerts: вход только через диалог настроек (authorization code)")
}

// Disable отменяет вход и забывает сессию в памяти; секрет удаляет Logout.
func (p *DonationAlertsProvider) Disable() {
	p.CancelAuth()
	p.mu.Lock()
	p.session = DonationAlertsSession{}
	p.hasSession = false
	p.socketToken = ""
	p.mu.Unlock()
}

// CancelAuth закрывает loopback-сервер и срывает ожидание callback'а.
func (p *DonationAlertsProvider) CancelAuth() {
	p.mu.Lock()
	cancel := p.cancelAuth
	p.mu.Unlock()
	if cancel != nil {
		cancel(context.Canceled)
	}
}

// BeginAuth поднимает loopback-сервер и возвращает ссылку для браузера.
// Сервер живёт до callback, CancelAuth или отмены контекста входа.
func (p *DonationAlertsProvider) BeginAuth(ctx context.Context) (DonationAlertsAuth, error) {
	if p.clientID == "" {
		return DonationAlertsAuth{}, errors.New("donationalerts: не задан client_id")
	}
	if p.clientSecret == "" {
		return DonationAlertsAuth{}, errors.New("donationalerts: не задан client_secret")
	}

	state, err := daRandomState()
	if err != nil {
		return DonationAlertsAuth{}, fmt.Errorf("donationalerts: генерация state: %w", err)
	}

	// p.mu держим весь setup: сервер надо записать в провайдер атомарно, иначе CancelAuth не найдёт cancel и сервер утечёт.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending {
		return DonationAlertsAuth{}, errors.New("donationalerts: вход уже идёт")
	}

	ln, err := net.Listen("tcp", p.listenAddr)
	if err != nil {
		return DonationAlertsAuth{}, fmt.Errorf("donationalerts: loopback-сервер: %w", err)
	}

	// Дополнительный слушатель — [::1] на том же порту: на Windows localhost нередко резолвится сначала
	// в ::1, и браузер, не дождавшись фолбэка на IPv4, просто не достучится. Ошибка не фатальна.
	ln6, err6 := daListenIPv6(ln)
	if err6 != nil {
		fmt.Println("donationalerts: IPv6-слушатель [::1] не поднят:", err6)
		ln6 = nil
	}

	redirect, err := p.effectiveRedirectURL(ln.Addr())
	if err != nil {
		_ = ln.Close()
		if ln6 != nil {
			_ = ln6.Close()
		}
		return DonationAlertsAuth{}, err
	}

	// Контекст входа: отмена пользователем (CancelAuth) или родителем закрывает сервер и срывает WaitAuth.
	authCtx, cancel := context.WithCancelCause(ctx)
	pending := &daPendingAuth{
		state:    state,
		redirect: redirect,
		resultCh: make(chan daCallback, 1),
		ctx:      authCtx,
		cancel:   cancel,
		ln:       ln,
		ln6:      ln6,
	}

	mux := http.NewServeMux()
	mux.HandleFunc(daRedirectPath(redirect), func(w http.ResponseWriter, r *http.Request) {
		p.handleCallback(pending, w, r)
	})
	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	pending.srv = srv

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			pending.send(daCallback{err: fmt.Errorf("donationalerts: loopback-сервер: %w", err)})
		}
	}()
	if ln6 != nil {
		// Тот же handler на втором слушателе: поломка IPv6 не должна ронять вход.
		go func() {
			if err := srv.Serve(ln6); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Println("donationalerts: IPv6-слушатель остановился:", err)
			}
		}()
	}
	go func() {
		<-authCtx.Done()
		_ = srv.Close()
	}()

	p.pending = true
	p.auth = pending
	p.cancelAuth = cancel

	return DonationAlertsAuth{
		AuthorizeURL: p.authorizeURLFor(redirect, state),
		RedirectURL:  redirect,
		State:        state,
	}, nil
}

// WaitAuth ждёт callback, обменивает код на токен и берёт профиль DA; блокирует до результата входа.
func (p *DonationAlertsProvider) WaitAuth(ctx context.Context, auth DonationAlertsAuth) (DonationAlertsSession, error) {
	p.mu.Lock()
	pending := p.auth
	p.mu.Unlock()
	if pending == nil {
		return DonationAlertsSession{}, errors.New("donationalerts: вход не начат")
	}
	if auth.State != "" && auth.State != pending.state {
		return DonationAlertsSession{}, errors.New("donationalerts: state не совпадает с начатым входом")
	}

	// Сервер закрываем в любом случае: вход либо завершился, либо отменён.
	defer p.finishAuth(pending)

	select {
	case cb := <-pending.resultCh:
		if cb.err != nil {
			return DonationAlertsSession{}, cb.err
		}
		// Сверка state: чужой callback — это CSRF, код из него не берём.
		if cb.state != pending.state {
			return DonationAlertsSession{}, errors.New("donationalerts: state не совпадает (возможна CSRF-атака)")
		}
		if cb.code == "" {
			return DonationAlertsSession{}, errors.New("donationalerts: в callback нет code")
		}
		return p.exchangeAndProfile(ctx, cb.code, pending.redirect)
	case <-ctx.Done():
		return DonationAlertsSession{}, ctx.Err()
	case <-pending.ctx.Done():
		return DonationAlertsSession{}, context.Cause(pending.ctx)
	}
}

// finishAuth снимает вход: закрывает loopback-сервер и чистит состояние.
func (p *DonationAlertsProvider) finishAuth(pending *daPendingAuth) {
	pending.cancel(context.Canceled)
	if pending.srv != nil {
		_ = pending.srv.Close()
	}
	// Слушатели закрываем явно: на IPv6-ветке goroutine могла не успеть зарегистрировать его в http.Server.
	if pending.ln != nil {
		_ = pending.ln.Close()
	}
	if pending.ln6 != nil {
		_ = pending.ln6.Close()
	}
	p.mu.Lock()
	if p.auth == pending {
		p.auth = nil
		p.pending = false
		p.cancelAuth = nil
	}
	p.mu.Unlock()
}

// handleCallback разбирает параметры от DA и отдаёт их ожидающему WaitAuth.
func (p *DonationAlertsProvider) handleCallback(pending *daPendingAuth, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	cb := daCallback{code: q.Get("code"), state: q.Get("state")}
	if code := q.Get("error"); code != "" {
		if desc := q.Get("error_description"); desc != "" {
			cb.err = fmt.Errorf("donationalerts: %s (%s)", code, desc)
		} else {
			cb.err = fmt.Errorf("donationalerts: %s", code)
		}
	}
	pending.send(cb)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if cb.err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, daErrorPage)
		return
	}
	_, _ = io.WriteString(w, daSuccessPage)
}

// exchangeAndProfile обменивает код на токен, читает профиль и сохраняет сессию.
func (p *DonationAlertsProvider) exchangeAndProfile(ctx context.Context, code, redirect string) (DonationAlertsSession, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("redirect_uri", redirect)
	form.Set("code", code)

	body, status, err := p.postForm(ctx, p.tokenURL, form)
	if err != nil {
		return DonationAlertsSession{}, err
	}
	if status != http.StatusOK {
		return DonationAlertsSession{}, fmt.Errorf("donationalerts: token endpoint (%d): %s", status, daErrorMessage(body))
	}

	var token struct {
		TokenType    string `json:"token_type"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return DonationAlertsSession{}, fmt.Errorf("donationalerts: token ответ (%d): %w", status, err)
	}
	if token.AccessToken == "" {
		return DonationAlertsSession{}, errors.New("donationalerts: token endpoint вернул пустой access_token")
	}
	if token.RefreshToken == "" {
		return DonationAlertsSession{}, errors.New("donationalerts: token endpoint вернул пустой refresh_token")
	}

	profile, err := p.fetchProfile(ctx, token.AccessToken)
	if err != nil {
		return DonationAlertsSession{}, err
	}

	ttl := daDefaultTokenTTL
	if token.ExpiresIn > 0 {
		ttl = time.Duration(token.ExpiresIn) * time.Second
	}
	session := DonationAlertsSession{
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		ExpiresAt:    time.Now().Add(ttl),
		UserID:       profile.ID,
		UserCode:     profile.Code,
	}

	p.mu.Lock()
	p.socketToken = profile.SocketConnectionToken
	p.mu.Unlock()

	// Токены — только в OS credential store; в settings.json остаются метаданные.
	if err := p.SaveSession(ctx, session); err != nil {
		return DonationAlertsSession{}, err
	}
	return session, nil
}

// fetchProfile читает профиль DA: GET /api/v1/user/oauth с Bearer-токеном; ответ обёрнут в {"data":{…}}.
func (p *DonationAlertsProvider) fetchProfile(ctx context.Context, accessToken string) (daProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.userURL, nil)
	if err != nil {
		return daProfile{}, fmt.Errorf("donationalerts: запрос %s: %w", p.userURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return daProfile{}, fmt.Errorf("donationalerts: %s: %w", p.userURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return daProfile{}, fmt.Errorf("donationalerts: чтение ответа %s: %w", p.userURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return daProfile{}, fmt.Errorf("donationalerts: user/oauth (%d): %s", resp.StatusCode, daErrorMessage(body))
	}

	var wrapper struct {
		Data daProfile `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return daProfile{}, fmt.Errorf("donationalerts: user/oauth ответ: %w", err)
	}
	if wrapper.Data.ID == 0 && wrapper.Data.Code == "" {
		return daProfile{}, errors.New("donationalerts: профиль пуст (проверьте скоуп oauth-user-show)")
	}
	return wrapper.Data, nil
}

// LoadSession читает сессию из OS credential store. found=false — входа нет.
func (p *DonationAlertsProvider) LoadSession(ctx context.Context) (DonationAlertsSession, bool, error) {
	if p.store == nil {
		return DonationAlertsSession{}, false, errors.New("donationalerts: не задан secret store")
	}
	p.storeMu.Lock()
	data, found, err := p.store.Load(DonationAlertsSessionSecretKey)
	p.storeMu.Unlock()
	if err != nil {
		return DonationAlertsSession{}, false, fmt.Errorf("donationalerts: загрузка сессии: %w", err)
	}
	if !found {
		return DonationAlertsSession{}, false, nil
	}
	var session DonationAlertsSession
	if err := json.Unmarshal(data, &session); err != nil {
		return DonationAlertsSession{}, false, fmt.Errorf("donationalerts: разбор сессии: %w", err)
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		return DonationAlertsSession{}, false, errors.New("donationalerts: неполная сессия в хранилище")
	}
	p.setSession(session)
	return session, true, nil
}

// SaveSession кладёт сессию в OS credential store и запоминает её в памяти.
func (p *DonationAlertsProvider) SaveSession(ctx context.Context, session DonationAlertsSession) error {
	if p.store == nil {
		return errors.New("donationalerts: не задан secret store")
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		return errors.New("donationalerts: нельзя сохранить неполную сессию")
	}
	// Эпоха растёт до захвата storeMu: новая сессия побеждает конкурентный Logout.
	p.bumpSessionEpoch()
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("donationalerts: сериализация сессии: %w", err)
	}
	p.storeMu.Lock()
	err = p.store.Save(DonationAlertsSessionSecretKey, data)
	p.storeMu.Unlock()
	if err != nil {
		return fmt.Errorf("donationalerts: сохранение сессии: %w", err)
	}
	p.setSession(session)
	return nil
}

// DeleteSession удаляет секрет из хранилища и забывает сессию в памяти.
func (p *DonationAlertsProvider) DeleteSession(ctx context.Context) error {
	// Эпоха растёт до захвата storeMu (как в SaveSession): удаление — тоже изменение сессии.
	p.bumpSessionEpoch()
	var err error
	p.storeMu.Lock()
	if p.store != nil {
		err = p.store.Delete(DonationAlertsSessionSecretKey)
	}
	p.storeMu.Unlock()
	p.clearSession()
	if err != nil {
		return fmt.Errorf("donationalerts: удаление сессии: %w", err)
	}
	return nil
}

// Logout удаляет сессию из хранилища: публичного revoke у DonationAlerts нет. Сессия, сменившаяся после
// снимка эпохи, не стирается (см. LogoutAt).
func (p *DonationAlertsProvider) Logout(ctx context.Context) error {
	session, ok := p.Session()
	if !ok {
		if loaded, found, err := p.LoadSession(ctx); err == nil && found {
			session, ok = loaded, true
		}
	}
	return p.LogoutAt(ctx, session, ok, p.SessionEpoch())
}

// LogoutAt — Logout с эпохой на момент выключения: изменилась эпоха — сессия
// не удаляется. Снимок сессии не используется: revoke у DA нет.
func (p *DonationAlertsProvider) LogoutAt(ctx context.Context, session DonationAlertsSession, hasSession bool, epoch uint64) error {
	return p.deleteSessionIfEpoch(epoch)
}

// deleteSessionIfEpoch удаляет секрет, только если эпоха не изменилась.
func (p *DonationAlertsProvider) deleteSessionIfEpoch(epoch uint64) error {
	// Удаление сериализуется storeMu, а не p.mu: эпоха растёт до захвата storeMu, поэтому устаревшее
	// удаление увидит новую эпоху и ничего не сделает.
	p.storeMu.Lock()
	defer p.storeMu.Unlock()

	p.mu.Lock()
	changed := p.sessionEpoch != epoch
	p.mu.Unlock()
	if changed {
		return nil
	}

	var err error
	if p.store != nil {
		if deleteErr := p.store.Delete(DonationAlertsSessionSecretKey); deleteErr != nil {
			err = fmt.Errorf("donationalerts: удаление сессии: %w", deleteErr)
		}
	}
	p.clearSession()
	return err
}

// RefreshSession обновляет access-токен refresh-грантом.
func (p *DonationAlertsProvider) RefreshSession(ctx context.Context) (DonationAlertsSession, error) {
	session, ok := p.Session()
	if !ok {
		loaded, found, err := p.LoadSession(ctx)
		if err != nil {
			return DonationAlertsSession{}, err
		}
		if !found {
			return DonationAlertsSession{}, errors.New("donationalerts: нет сессии для обновления")
		}
		session = loaded
	}
	if session.RefreshToken == "" {
		return DonationAlertsSession{}, errors.New("donationalerts: в сессии нет refresh-токена")
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", session.RefreshToken)
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("scope", DonationAlertsScope)

	body, status, err := p.postForm(ctx, p.tokenURL, form)
	if err != nil {
		return DonationAlertsSession{}, err
	}
	if status != http.StatusOK {
		return DonationAlertsSession{}, fmt.Errorf("donationalerts: refresh (%d): %s", status, daErrorMessage(body))
	}

	var token struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &token); err != nil {
		return DonationAlertsSession{}, fmt.Errorf("donationalerts: refresh ответ (%d): %w", status, err)
	}
	if token.AccessToken == "" {
		return DonationAlertsSession{}, errors.New("donationalerts: refresh вернул пустой access_token")
	}

	updated := session
	updated.AccessToken = token.AccessToken
	// DA может не прислать новый refresh-токен — старый остаётся в силе.
	if token.RefreshToken != "" {
		updated.RefreshToken = token.RefreshToken
	}
	ttl := daDefaultTokenTTL
	if token.ExpiresIn > 0 {
		ttl = time.Duration(token.ExpiresIn) * time.Second
	}
	updated.ExpiresAt = time.Now().Add(ttl)

	if err := p.SaveSession(ctx, updated); err != nil {
		return DonationAlertsSession{}, err
	}
	return updated, nil
}

// Session — текущая сессия в памяти (второй элемент — есть ли она).
func (p *DonationAlertsProvider) Session() (DonationAlertsSession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session, p.hasSession
}

// HTTPClient — HTTP-клиент провайдера.
func (p *DonationAlertsProvider) HTTPClient() *http.Client {
	return p.http
}

// ClientCredentials — client_id и client_secret приложения DA.
func (p *DonationAlertsProvider) ClientCredentials() (clientID, clientSecret string) {
	return p.clientID, p.clientSecret
}

// Endpoints — адреса API DA для слоя событий.
func (p *DonationAlertsProvider) Endpoints() (tokenURL, userURL, subscribeURL, wsURL string) {
	return p.tokenURL, p.userURL, p.subscribeURL, p.wsURL
}

// emit отправляет событие подписчику.
func (p *DonationAlertsProvider) emit(eventType EventType, data any) {
	p.mu.Lock()
	handler := p.handler
	p.mu.Unlock()
	if handler == nil {
		return
	}
	handler(eventType, data)
}

// SessionEpoch — счётчик изменений сессии: LogoutAt сверяет снимок, чтобы старый выход не стёр новую сессию.
func (p *DonationAlertsProvider) SessionEpoch() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionEpoch
}

func (p *DonationAlertsProvider) bumpSessionEpoch() {
	p.mu.Lock()
	p.sessionEpoch++
	p.mu.Unlock()
}

func (p *DonationAlertsProvider) setSession(session DonationAlertsSession) {
	p.mu.Lock()
	p.session = session
	p.hasSession = true
	p.sessionEpoch++
	p.mu.Unlock()
}

func (p *DonationAlertsProvider) clearSession() {
	p.mu.Lock()
	p.session = DonationAlertsSession{}
	p.hasSession = false
	p.socketToken = ""
	p.mu.Unlock()
}

func (p *DonationAlertsProvider) postForm(ctx context.Context, endpoint string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, fmt.Errorf("donationalerts: запрос %s: %w", endpoint, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("donationalerts: %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("donationalerts: чтение ответа %s: %w", endpoint, err)
	}
	return body, resp.StatusCode, nil
}

// authorizeURLFor собирает ссылку для браузера; scope отдаём через пробел, как требует DA.
func (p *DonationAlertsProvider) authorizeURLFor(redirect, state string) string {
	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirect)
	q.Set("scope", DonationAlertsScope)
	q.Set("state", state)
	return p.authorizeURL + "?" + q.Encode()
}

// effectiveRedirectURL — настроенный redirect_uri с портом, на котором реально слушает loopback-сервер.
func (p *DonationAlertsProvider) effectiveRedirectURL(addr net.Addr) (string, error) {
	u, err := url.Parse(p.redirectURL)
	if err != nil {
		return "", fmt.Errorf("donationalerts: redirect_url: %w", err)
	}
	if _, port, err := net.SplitHostPort(addr.String()); err == nil && port != "" {
		u.Host = net.JoinHostPort(u.Hostname(), port)
	}
	return u.String(), nil
}

// daListenIPv6 — дополнительный слушатель на [::1] с тем же портом, что и основной (в том числе
// эфемерным): localhost на Windows нередко резолвится сначала в ::1, и без него браузер может не
// достучаться до callback'а.
func daListenIPv6(primary net.Listener) (net.Listener, error) {
	_, port, err := net.SplitHostPort(primary.Addr().String())
	if err != nil || port == "" {
		return nil, fmt.Errorf("в адресе %q нет порта", primary.Addr())
	}
	return net.Listen("tcp", net.JoinHostPort("::1", port))
}

// daRedirectPath — путь callback'а из redirect_uri.
func daRedirectPath(redirect string) string {
	u, err := url.Parse(redirect)
	if err != nil || u.Path == "" {
		return DonationAlertsRedirectPath
	}
	return u.Path
}

// daRandomState — случайный state для защиты от CSRF: криптослучайные байты в base64url.
func daRandomState() (string, error) {
	b := make([]byte, daStateBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// daErrorMessage вытаскивает текст ошибки из ответа DA (error/error_description), иначе — сырой ответ.
func daErrorMessage(body []byte) string {
	var e struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
		Message     string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)

	text := e.Error
	if text == "" {
		text = e.Message
	}
	if e.Description != "" {
		if text == "" {
			text = e.Description
		} else {
			text = text + ": " + e.Description
		}
	}
	if text == "" {
		text = strings.TrimSpace(string(body))
	}

	// Не тащим в UI мегабайт HTML от прокси или captive portal.
	const maxRunes = 300
	runes := []rune(text)
	if len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + "…"
	}
	return text
}
