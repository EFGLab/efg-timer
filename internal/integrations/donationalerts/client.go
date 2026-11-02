package donationalerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
)

// DefaultUserURL — профиль DA: socket_connection_token + user_id (e05s01-t2).
const DefaultUserURL = "https://www.donationalerts.com/api/v1/user/oauth"

// DefaultCentrifugoSubscribeURL — REST-подписка на каналы Centrifugo (e05s02-t1):
// POST {channels, client} → токены каналов.
const DefaultCentrifugoSubscribeURL = "https://www.donationalerts.com/api/v1/centrifuge/subscribe"

// Client — клиент API DonationAlerts: Bearer-токен, автообновление при 401.
type Client struct {
	opts ClientOptions
	mu   sync.Mutex // защита Session: авто-refresh из разных горутин (e05s02)
}

// ClientOptions — параметры клиента. TokenURL/UserURL переопределяются
// в тестах; Session задаётся после OAuth-входа.
type ClientOptions struct {
	ClientID     string
	ClientSecret string
	Session      Session
	TokenURL     string // пусто → DefaultTokenURL
	UserURL      string // пусто → DefaultUserURL
	SubscribeURL string // пусто → DefaultCentrifugoSubscribeURL (e05s02-t1)
	HTTPClient   *http.Client
	OnRefresh    func(Session) // хук после успешного авто-refresh (main сохраняет конфиг)
}

// NewClient готовит клиента: подставляет дефолты эндпоинтов и HTTP.
func NewClient(opts ClientOptions) *Client {
	if opts.TokenURL == "" {
		opts.TokenURL = DefaultTokenURL
	}
	if opts.UserURL == "" {
		opts.UserURL = DefaultUserURL
	}
	if opts.SubscribeURL == "" {
		opts.SubscribeURL = DefaultCentrifugoSubscribeURL
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	return &Client{opts: opts}
}

// Session — копия текущей сессии (потокобезопасно); после SocketToken
// содержит socket_connection_token и user_id в памяти.
func (c *Client) Session() Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opts.Session
}

// Refresh обновляет пару токенов refresh-грантом и хранит её в клиенте;
// OnRefresh вызывается после успешного обновления.
func (c *Client) Refresh(ctx context.Context) error {
	sess := c.Session()
	newSess, err := RefreshSession(ctx, OAuthOptions{
		ClientID:     c.opts.ClientID,
		ClientSecret: c.opts.ClientSecret,
		TokenURL:     c.opts.TokenURL,
	}, sess)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.opts.Session = newSess
	c.mu.Unlock()
	if c.opts.OnRefresh != nil {
		c.opts.OnRefresh(newSess)
	}
	return nil
}

// doRequest — запрос с Bearer; при 401 один раз обновляет токен и повторяет
// запрос (преследование подвисших сессий после рестарта). Тело — []byte:
// повторная отправка требует перезапускаемого тела.
func (c *Client) doRequest(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	resp, err := c.rawRequest(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}
	resp.Body.Close()
	if err := c.Refresh(ctx); err != nil {
		return nil, err
	}
	return c.rawRequest(ctx, method, url, body)
}

func (c *Client) rawRequest(ctx context.Context, method, url string, body []byte) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, fmt.Errorf("donationalerts: request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.Session().AccessToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("donationalerts: %s: %w", url, err)
	}
	return resp, nil
}

// SubscribeCentrifuge — POST /api/v1/centrifuge/subscribe {channels, client}
// → токен канала для client-side WS-подписки (e05s02-t1).
func (c *Client) SubscribeCentrifuge(ctx context.Context, channel, clientID string) (string, error) {
	body, err := json.Marshal(map[string]any{"channels": []string{channel}, "client": clientID})
	if err != nil {
		return "", fmt.Errorf("donationalerts: subscribe: %w", err)
	}
	resp, err := c.doRequest(ctx, http.MethodPost, c.opts.SubscribeURL, body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("donationalerts: subscribe: статус %d", resp.StatusCode)
	}
	var r struct {
		Channels []struct {
			Channel string `json:"channel"`
			Token   string `json:"token"`
		} `json:"channels"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("donationalerts: subscribe: %w", err)
	}
	if len(r.Channels) == 0 || r.Channels[0].Token == "" {
		return "", errors.New("donationalerts: subscribe не вернул токен канала")
	}
	return r.Channels[0].Token, nil
}

// SocketToken — GET /api/v1/user/oauth: socket_connection_token для
// Centrifugo и user_id (канал $alerts:donation_<user_id>, e05s02).
// Оба значения хранятся в памяти сессии (json:"-" — в конфиг не пишутся).
func (c *Client) SocketToken(ctx context.Context) (string, int64, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, c.opts.UserURL, nil)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	var u struct {
		ID                    int64  `json:"id"`
		SocketConnectionToken string `json:"socket_connection_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", 0, fmt.Errorf("donationalerts: user/oauth (%d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("donationalerts: user/oauth: статус %d", resp.StatusCode)
	}
	if u.SocketConnectionToken == "" {
		return "", 0, errors.New("donationalerts: пустой socket_connection_token (проверьте скоуп oauth-user-show)")
	}
	c.mu.Lock()
	c.opts.Session.SocketConnectionToken = u.SocketConnectionToken
	c.opts.Session.UserID = u.ID
	c.mu.Unlock()
	return u.SocketConnectionToken, u.ID, nil
}
