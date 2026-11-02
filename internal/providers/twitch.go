package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	twitchapi "github.com/EFGLab/efg-timer/internal/integrations/twitch"
)

const (
	ProviderNameTwitch ProviderName = "Twitch"

	// TwitchSessionSecretKey — ключ сессии в OS credential store.
	TwitchSessionSecretKey = "twitch.session"

	TwitchDeviceURL = "https://id.twitch.tv/oauth2/device"
	TwitchRevokeURL = "https://id.twitch.tv/oauth2/revoke"

	// Скоуп общий с legacy-клиентом: берём его константу.
	TwitchScopeSubscriptions = twitchapi.ScopeSubscriptions
)

// SecretStore — абстракция над OS credential store.
type SecretStore interface {
	Load(key string) (value []byte, found bool, err error)
	Save(key string, value []byte) error
	Delete(key string) error
}

// TwitchSession — данные входа Twitch; ExpiresAt относится к access-токену.
type TwitchSession struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
}

// TwitchUser — канал владельца токена из helix/users.
type TwitchUser struct {
	ID          string `json:"id"`
	Login       string `json:"login"`
	DisplayName string `json:"display_name"`
}

// TwitchDeviceAuth — результат первого шага Device Code Grant Flow.
type TwitchDeviceAuth struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       time.Duration
	Interval        time.Duration
}

// TwitchOptions — параметры провайдера; URL'ы и PollInterval нужны тестам.
type TwitchOptions struct {
	ClientID   string
	Store      SecretStore
	HTTPClient *http.Client
	DeviceURL  string
	TokenURL   string
	UsersURL   string
	RevokeURL  string
	// EventSubURL — WS-эндпоинт EventSub; пусто → twitchapi.DefaultEventSubURL.
	EventSubURL string
	// EventSubSubscribeURL — Helix-эндпоинт подписок EventSub; пусто → twitchapi.DefaultEventSubSubscribeURL.
	EventSubSubscribeURL string
	PollInterval         time.Duration
}

// twitchDefaultTokenTTL — срок жизни access-токена, если Twitch не прислал expires_in.
const twitchDefaultTokenTTL = 4 * time.Hour

var _ Provider = (*TwitchProvider)(nil)

type TwitchProvider struct {
	clientID string
	store    SecretStore
	http     *http.Client

	deviceURL string
	tokenURL  string
	usersURL  string
	revokeURL string

	eventSubURL          string
	eventSubSubscribeURL string

	pollInterval time.Duration

	// storeMu сериализует обращения к OS credential store: держать p.mu во
	// время вызовов хранилища нельзя (его читают обработчики на frame thread).
	storeMu      sync.Mutex
	mu           sync.Mutex
	cancelAuth   context.CancelFunc
	pending      bool
	session      TwitchSession
	hasSession   bool
	sessionEpoch uint64 // счётчик изменений сессии: LogoutAt сверяет снимок

	// handler — подписчик на события; вызывается вне p.mu.
	handler EventHandler

	// eventsCancel останавливает поток событий, eventsGen отличает текущий
	// поток от предыдущего, eventsRunning — признак работающего потока.
	eventsCancel  context.CancelFunc
	eventsGen     uint64
	eventsRunning bool
}

func NewTwitchProvider(opts TwitchOptions) *TwitchProvider {
	p := &TwitchProvider{
		clientID:             strings.TrimSpace(opts.ClientID),
		store:                opts.Store,
		http:                 opts.HTTPClient,
		deviceURL:            opts.DeviceURL,
		tokenURL:             opts.TokenURL,
		usersURL:             opts.UsersURL,
		revokeURL:            opts.RevokeURL,
		eventSubURL:          opts.EventSubURL,
		eventSubSubscribeURL: opts.EventSubSubscribeURL,
		pollInterval:         opts.PollInterval,
	}
	if p.http == nil {
		p.http = &http.Client{Timeout: 30 * time.Second}
	}
	if p.deviceURL == "" {
		p.deviceURL = TwitchDeviceURL
	}
	if p.tokenURL == "" {
		p.tokenURL = twitchapi.DefaultTokenURL
	}
	if p.usersURL == "" {
		p.usersURL = twitchapi.DefaultUsersURL
	}
	if p.revokeURL == "" {
		p.revokeURL = TwitchRevokeURL
	}
	if p.eventSubURL == "" {
		p.eventSubURL = twitchapi.DefaultEventSubURL
	}
	if p.eventSubSubscribeURL == "" {
		p.eventSubSubscribeURL = twitchapi.DefaultEventSubSubscribeURL
	}
	return p
}

func (p *TwitchProvider) GetName() ProviderName { return ProviderNameTwitch }

// On сохраняет обработчик событий провайдера.
func (p *TwitchProvider) On(handler EventHandler) {
	p.mu.Lock()
	p.handler = handler
	p.mu.Unlock()
}

// emit вызывает обработчик вне p.mu: обработчик ходит в провайдер.
func (p *TwitchProvider) emit(eventType EventType, data any) {
	p.mu.Lock()
	handler := p.handler
	p.mu.Unlock()
	if handler != nil {
		handler(eventType, data)
	}
}

func (p *TwitchProvider) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pending
}

// Enable запрещён: device flow без UI бессмыслен, UI вызывает BeginDeviceAuth/PollDeviceAuth.
func (p *TwitchProvider) Enable(ctx context.Context) (string, error) {
	return "", errors.New("twitch: вход только через диалог настроек (device code flow)")
}

// Disable отменяет вход и забывает сессию в памяти; секрет удаляет Logout.
func (p *TwitchProvider) Disable() {
	p.CancelAuth()
	p.mu.Lock()
	p.session = TwitchSession{}
	p.hasSession = false
	p.mu.Unlock()
}

func (p *TwitchProvider) CancelAuth() {
	p.mu.Lock()
	cancel := p.cancelAuth
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// BeginDeviceAuth запрашивает device_code и user_code.
func (p *TwitchProvider) BeginDeviceAuth(ctx context.Context) (TwitchDeviceAuth, error) {
	if p.clientID == "" {
		return TwitchDeviceAuth{}, errors.New("twitch: не задан client_id")
	}

	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("scopes", TwitchScopeSubscriptions)

	body, status, err := p.postForm(ctx, p.deviceURL, form)
	if err != nil {
		return TwitchDeviceAuth{}, err
	}

	var resp struct {
		DeviceCode      string `json:"device_code"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		Message         string `json:"message"`
		Error           string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return TwitchDeviceAuth{}, fmt.Errorf("twitch: device ответ (%d): %w", status, err)
	}
	if status != http.StatusOK {
		return TwitchDeviceAuth{}, fmt.Errorf("twitch: device endpoint (%d): %s", status, twitchErrorMessage(resp.Message, resp.Error, body))
	}
	if resp.DeviceCode == "" || resp.UserCode == "" || resp.VerificationURI == "" {
		return TwitchDeviceAuth{}, errors.New("twitch: device endpoint вернул неполный ответ")
	}

	expiresIn := time.Duration(resp.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = 30 * time.Minute
	}
	interval := time.Duration(resp.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}

	return TwitchDeviceAuth{
		DeviceCode:      resp.DeviceCode,
		UserCode:        resp.UserCode,
		VerificationURI: resp.VerificationURI,
		ExpiresIn:       expiresIn,
		Interval:        interval,
	}, nil
}

// PollDeviceAuth опрашивает token endpoint до подтверждения кода, отмены или истечения срока device_code.
func (p *TwitchProvider) PollDeviceAuth(ctx context.Context, auth TwitchDeviceAuth) (TwitchSession, error) {
	if auth.DeviceCode == "" {
		return TwitchSession{}, errors.New("twitch: пустой device_code")
	}

	pollCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	if p.pending {
		p.mu.Unlock()
		cancel()
		return TwitchSession{}, errors.New("twitch: авторизация уже идёт")
	}
	p.pending = true
	p.cancelAuth = cancel
	p.mu.Unlock()

	defer func() {
		cancel()
		p.mu.Lock()
		p.pending = false
		p.cancelAuth = nil
		p.mu.Unlock()
	}()

	interval := auth.Interval
	if p.pollInterval > 0 {
		interval = p.pollInterval
	}
	if interval <= 0 {
		interval = 5 * time.Second
	}
	expiresIn := auth.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 30 * time.Minute
	}
	expiresAt := time.Now().Add(expiresIn)

	for {
		if !time.Now().Before(expiresAt) {
			return TwitchSession{}, errors.New("twitch: срок действия кода истёк")
		}

		body, status, err := p.pollOnce(pollCtx, auth.DeviceCode)
		if err != nil {
			if pollCtx.Err() != nil {
				return TwitchSession{}, pollCtx.Err()
			}
			return TwitchSession{}, err
		}

		if status == http.StatusOK {
			var resp struct {
				AccessToken  string   `json:"access_token"`
				RefreshToken string   `json:"refresh_token"`
				ExpiresIn    int      `json:"expires_in"`
				Scope        []string `json:"scope"`
			}
			if err := json.Unmarshal(body, &resp); err != nil {
				return TwitchSession{}, fmt.Errorf("twitch: token ответ (%d): %w", status, err)
			}
			if resp.AccessToken == "" || resp.RefreshToken == "" {
				return TwitchSession{}, errors.New("twitch: token endpoint вернул неполный токен")
			}
			// expires_in по RFC 8628 необязателен; берём типичные 4 часа.
			expiresIn := resp.ExpiresIn
			if expiresIn <= 0 {
				expiresIn = int(twitchDefaultTokenTTL / time.Second)
			}
			session := TwitchSession{
				AccessToken:  resp.AccessToken,
				RefreshToken: resp.RefreshToken,
				ExpiresAt:    time.Now().Add(time.Duration(expiresIn) * time.Second),
				Scopes:       resp.Scope,
			}
			p.setSession(session)
			return session, nil
		}

		var er struct {
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(body, &er)
		code := twitchErrorMessage(er.Message, er.Error, body)

		switch code {
		case "authorization_pending":
			// Пользователь ещё не подтвердил код.
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return TwitchSession{}, errors.New("twitch: вход отклонён пользователем")
		case "expired_token":
			return TwitchSession{}, errors.New("twitch: срок действия кода истёк")
		default:
			return TwitchSession{}, fmt.Errorf("twitch: авторизация (%d): %s", status, code)
		}

		timer := time.NewTimer(interval)
		select {
		case <-pollCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return TwitchSession{}, pollCtx.Err()
		case <-timer.C:
		}
	}
}

func (p *TwitchProvider) pollOnce(ctx context.Context, deviceCode string) ([]byte, int, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("scopes", TwitchScopeSubscriptions)
	form.Set("device_code", deviceCode)
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	return p.postForm(ctx, p.tokenURL, form)
}

// FetchUser запрашивает helix/users; обновлённая сессия сохраняется здесь же, даже если профиль упал.
func (p *TwitchProvider) FetchUser(ctx context.Context, session TwitchSession) (TwitchUser, TwitchSession, error) {
	client := twitchapi.NewClient(twitchapi.ClientOptions{
		ClientID:   p.clientID,
		Session:    toLegacySession(session),
		TokenURL:   p.tokenURL,
		UsersURL:   p.usersURL,
		HTTPClient: p.http,
	})

	user, err := client.FetchUser(ctx)
	updated := fromLegacySession(client.Session(), session)
	if updated.AccessToken != session.AccessToken || updated.RefreshToken != session.RefreshToken {
		updated.ExpiresAt = twitchExpiresAt(client.Session().ExpiresIn)
		updated.Scopes = session.Scopes
		p.setSession(updated)

		// Twitch ротирует refresh-токены: сохраняем новую сессию, даже если запрос упал.
		if saveErr := p.SaveSession(ctx, updated); saveErr != nil {
			if err != nil {
				return TwitchUser{}, updated, errors.Join(err, saveErr)
			}
			return TwitchUser{}, updated, saveErr
		}
	}

	if err != nil {
		return TwitchUser{}, updated, err
	}
	return TwitchUser{
		ID:          user.ID,
		Login:       user.Login,
		DisplayName: user.DisplayName,
	}, updated, nil
}

// LoadSession читает сессию из OS credential store. found=false — входа нет.
func (p *TwitchProvider) LoadSession(ctx context.Context) (TwitchSession, bool, error) {
	if p.store == nil {
		return TwitchSession{}, false, errors.New("twitch: не задан secret store")
	}
	data, found, err := p.store.Load(TwitchSessionSecretKey)
	if err != nil {
		return TwitchSession{}, false, fmt.Errorf("twitch: загрузка сессии: %w", err)
	}
	if !found {
		return TwitchSession{}, false, nil
	}
	var session TwitchSession
	if err := json.Unmarshal(data, &session); err != nil {
		return TwitchSession{}, false, fmt.Errorf("twitch: разбор сессии: %w", err)
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		return TwitchSession{}, false, errors.New("twitch: неполная сессия в хранилище")
	}
	p.setSession(session)
	return session, true, nil
}

func (p *TwitchProvider) SaveSession(ctx context.Context, session TwitchSession) error {
	if p.store == nil {
		return errors.New("twitch: не задан secret store")
	}
	if session.AccessToken == "" || session.RefreshToken == "" {
		return errors.New("twitch: нельзя сохранить неполную сессию")
	}
	// Эпоха растёт до захвата storeMu: новая сессия побеждает конкурентный Logout.
	p.bumpSessionEpoch()
	data, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("twitch: сериализация сессии: %w", err)
	}
	p.storeMu.Lock()
	err = p.store.Save(TwitchSessionSecretKey, data)
	p.storeMu.Unlock()
	if err != nil {
		return fmt.Errorf("twitch: сохранение сессии: %w", err)
	}
	p.setSession(session)
	return nil
}

func (p *TwitchProvider) DeleteSession(ctx context.Context) error {
	// Эпоха растёт до захвата storeMu (как в SaveSession): удаление — тоже изменение сессии.
	p.bumpSessionEpoch()
	defer p.clearSession()
	if p.store == nil {
		return nil
	}
	p.storeMu.Lock()
	err := p.store.Delete(TwitchSessionSecretKey)
	p.storeMu.Unlock()
	if err != nil {
		return fmt.Errorf("twitch: удаление сессии: %w", err)
	}
	return nil
}

// Logout отзывает токен (best effort) и удаляет сессию из хранилища.
func (p *TwitchProvider) Logout(ctx context.Context) error {
	session, ok := p.Session()
	if !ok {
		if loaded, found, err := p.LoadSession(ctx); err == nil && found {
			session, ok = loaded, true
		}
	}
	return p.LogoutAt(ctx, session, ok, p.SessionEpoch())
}

// LogoutAt — Logout с эпохой на момент выключения: изменилась эпоха — сессия не удаляется.
func (p *TwitchProvider) LogoutAt(ctx context.Context, session TwitchSession, hasSession bool, epoch uint64) error {
	var revokeErr error
	if hasSession {
		revokeErr = p.Revoke(ctx, session)
	}

	return errors.Join(revokeErr, p.deleteSessionIfEpoch(epoch))
}

func (p *TwitchProvider) deleteSessionIfEpoch(epoch uint64) error {
	// Удаление сериализуется storeMu, а не p.mu: эпоха растёт до захвата
	// storeMu, поэтому устаревшее удаление увидит новую эпоху и ничего не
	// сделает. p.mu на время Delete не держим.
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
		if deleteErr := p.store.Delete(TwitchSessionSecretKey); deleteErr != nil {
			err = fmt.Errorf("twitch: удаление сессии: %w", deleteErr)
		}
	}
	p.clearSession()
	return err
}

// Revoke отзывает токен best effort: сначала access, при ошибке — refresh.
func (p *TwitchProvider) Revoke(ctx context.Context, session TwitchSession) error {
	if session.AccessToken == "" {
		return p.revokeToken(ctx, session.RefreshToken)
	}
	err := p.revokeToken(ctx, session.AccessToken)
	if err == nil || session.RefreshToken == "" {
		return err
	}
	if refreshErr := p.revokeToken(ctx, session.RefreshToken); refreshErr != nil {
		return errors.Join(err, refreshErr)
	}
	return nil
}

func (p *TwitchProvider) revokeToken(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}

	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("token", token)

	body, status, err := p.postForm(ctx, p.revokeURL, form)
	if err != nil {
		return err
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return fmt.Errorf("twitch: revoke (%d): %s", status, strings.TrimSpace(string(body)))
	}
	return nil
}

func (p *TwitchProvider) Session() (TwitchSession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session, p.hasSession
}

func (p *TwitchProvider) SessionEpoch() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionEpoch
}

func (p *TwitchProvider) bumpSessionEpoch() {
	p.mu.Lock()
	p.sessionEpoch++
	p.mu.Unlock()
}

func (p *TwitchProvider) setSession(session TwitchSession) {
	p.mu.Lock()
	p.session = session
	p.hasSession = true
	p.sessionEpoch++
	p.mu.Unlock()
}

func (p *TwitchProvider) clearSession() {
	p.mu.Lock()
	p.session = TwitchSession{}
	p.hasSession = false
	p.mu.Unlock()
}

func (p *TwitchProvider) postForm(ctx context.Context, endpoint string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, fmt.Errorf("twitch: запрос %s: %w", endpoint, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("twitch: %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("twitch: чтение ответа %s: %w", endpoint, err)
	}
	return body, resp.StatusCode, nil
}

// toLegacySession — снимок сессии для legacy-клиента: ExpiresIn у него — остаток до ExpiresAt, а не TTL.
func toLegacySession(session TwitchSession) twitchapi.TwitchSession {
	expiresIn := 0
	if !session.ExpiresAt.IsZero() {
		if d := time.Until(session.ExpiresAt); d > 0 {
			expiresIn = int(d.Seconds())
		}
	}
	return twitchapi.TwitchSession{
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ExpiresIn:    expiresIn,
	}
}

// twitchExpiresAt — срок действия access-токена по expires_in клиента.
// Нулевое значение брать нельзя: ExpiresIn в снимке сессии — остаток, а не TTL.
func twitchExpiresAt(expiresIn int) time.Time {
	ttl := twitchDefaultTokenTTL
	if expiresIn > 0 {
		ttl = time.Duration(expiresIn) * time.Second
	}
	return time.Now().Add(ttl)
}

func fromLegacySession(session twitchapi.TwitchSession, old TwitchSession) TwitchSession {
	return TwitchSession{
		AccessToken:  session.AccessToken,
		RefreshToken: session.RefreshToken,
		ExpiresAt:    old.ExpiresAt,
		Scopes:       old.Scopes,
	}
}

func twitchErrorMessage(message, errText string, body []byte) string {
	text := message
	if text == "" {
		text = errText
	}
	if text == "" {
		text = string(body)
	}
	text = strings.TrimSpace(text)

	// Не тащим в UI мегабайт HTML от прокси или captive portal.
	const maxRunes = 300
	runes := []rune(text)
	if len(runes) > maxRunes {
		text = string(runes[:maxRunes]) + "…"
	}
	return text
}
