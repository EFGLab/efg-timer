package twitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// DefaultUsersURL — helix/users: канал владельца токена (login для статуса
// в GUI, e04s01-t3).
const DefaultUsersURL = "https://api.twitch.tv/helix/users"

// DefaultEventSubSubscribeURL — helix/eventsub/subscriptions: POST подписок
// websocket-транспорта (e04s02-t2).
const DefaultEventSubSubscribeURL = "https://api.twitch.tv/helix/eventsub/subscriptions"

// errUnauthorized — маркер 401 от Helix: токен протух, нужен refresh.
var errUnauthorized = errors.New("twitch: 401")

// ClientOptions — параметры клиента Helix. TokenURL/UsersURL переопределяются
// в тестах; OnRefresh вызывается после каждого успешного обновления токена
// (для записи в конфиг).
type ClientOptions struct {
	ClientID         string
	Session          TwitchSession
	TokenURL         string // пусто → DefaultTokenURL
	UsersURL         string // пусто → DefaultUsersURL
	SubscriptionsURL string // пусто → DefaultEventSubSubscribeURL (e04s02-t2)
	HTTPClient       *http.Client
	OnRefresh        func(TwitchSession)
}

// Client — клиент Helix с текущим сеансом и автообновлением токена при 401
// (e04s01-t2). Безопасен для использования из нескольких горутин.
type Client struct {
	clientID         string
	tokenURL         string
	usersURL         string
	subscriptionsURL string
	http             *http.Client
	onRefresh        func(TwitchSession)

	mu      sync.Mutex
	session TwitchSession
}

// NewClient создаёт клиент; недостающие параметры добираются дефолтом.
func NewClient(opts ClientOptions) *Client {
	c := &Client{
		clientID:         opts.ClientID,
		tokenURL:         opts.TokenURL,
		usersURL:         opts.UsersURL,
		subscriptionsURL: opts.SubscriptionsURL,
		http:             opts.HTTPClient,
		onRefresh:        opts.OnRefresh,
		session:          opts.Session,
	}
	if c.tokenURL == "" {
		c.tokenURL = DefaultTokenURL
	}
	if c.usersURL == "" {
		c.usersURL = DefaultUsersURL
	}
	if c.subscriptionsURL == "" {
		c.subscriptionsURL = DefaultEventSubSubscribeURL
	}
	if c.http == nil {
		c.http = http.DefaultClient
	}
	return c
}

// Session — снимок текущего сеанса (для записи в конфиг).
func (c *Client) Session() TwitchSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

func (c *Client) setSession(s TwitchSession) {
	c.mu.Lock()
	c.session = s
	c.mu.Unlock()
	if c.onRefresh != nil {
		c.onRefresh(s)
	}
}

// Refresh обновляет токен refresh-грантом, сохраняет новый сеанс в клиенте
// и отдаёт его в OnRefresh.
func (c *Client) Refresh(ctx context.Context) (TwitchSession, error) {
	c.mu.Lock()
	refresh := c.session.RefreshToken
	c.mu.Unlock()
	if refresh == "" {
		return TwitchSession{}, errors.New("twitch: нет refresh_token")
	}
	v := url.Values{}
	v.Set("client_id", c.clientID)
	v.Set("grant_type", "refresh_token")
	v.Set("refresh_token", refresh)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(v.Encode()))
	if err != nil {
		return TwitchSession{}, fmt.Errorf("twitch: refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return TwitchSession{}, fmt.Errorf("twitch: token endpoint: %w", err)
	}
	defer resp.Body.Close()
	var tr struct {
		TwitchSession
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return TwitchSession{}, fmt.Errorf("twitch: refresh ответ (%d): %w", resp.StatusCode, err)
	}
	if tr.Error != "" {
		return TwitchSession{}, fmt.Errorf("twitch: refresh: %s (%s)", tr.Error, tr.ErrorDescription)
	}
	if resp.StatusCode != http.StatusOK {
		return TwitchSession{}, fmt.Errorf("twitch: refresh: статус %d", resp.StatusCode)
	}
	if tr.AccessToken == "" {
		return TwitchSession{}, errors.New("twitch: refresh: пустой access_token")
	}
	c.setSession(tr.TwitchSession)
	return tr.TwitchSession, nil
}

// TwitchUser — канал из helix/users.
type TwitchUser struct {
	ID          string `json:"id"`
	Login       string `json:"login"`
	DisplayName string `json:"display_name"`
}

// FetchUser — канал владельца токена. При 401 токен обновляется и запрос
// повторяется один раз.
func (c *Client) FetchUser(ctx context.Context) (TwitchUser, error) {
	body, err := c.doJSON(ctx, c.usersURL)
	if err != nil {
		return TwitchUser{}, err
	}
	var ur struct {
		Data []TwitchUser `json:"data"`
	}
	if err := json.Unmarshal(body, &ur); err != nil {
		return TwitchUser{}, fmt.Errorf("twitch: users ответ: %w", err)
	}
	if len(ur.Data) == 0 {
		return TwitchUser{}, errors.New("twitch: users: пустой data")
	}
	return ur.Data[0], nil
}

// doJSON выполняет авторизованный запрос; при 401 обновляет токен и повторяет
// запрос один раз.
func (c *Client) doJSON(ctx context.Context, url string) ([]byte, error) {
	body, err := c.attempt(ctx, http.MethodGet, url, nil, c.Session().AccessToken)
	if errors.Is(err, errUnauthorized) {
		if _, rerr := c.Refresh(ctx); rerr != nil {
			return nil, rerr
		}
		body, err = c.attempt(ctx, http.MethodGet, url, nil, c.Session().AccessToken)
	}
	return body, err
}

// postJSON выполняет авторизованный POST с JSON-телом; при 401 обновляет
// токен и повторяет запрос один раз.
func (c *Client) postJSON(ctx context.Context, url string, body []byte) ([]byte, error) {
	data, err := c.attempt(ctx, http.MethodPost, url, body, c.Session().AccessToken)
	if errors.Is(err, errUnauthorized) {
		if _, rerr := c.Refresh(ctx); rerr != nil {
			return nil, rerr
		}
		data, err = c.attempt(ctx, http.MethodPost, url, body, c.Session().AccessToken)
	}
	return data, err
}

// eventSubSubscriptions — типы подписок EventSub v1 (e04s02-t2): новые сабы
// (включая подаренные) и ре-сабы.
var eventSubSubscriptions = []string{"channel.subscribe", "channel.subscription.message"}

// SubscribeEventSub подписывает websocket-транспорт sessionID на события
// канала broadcasterID (channel.subscribe + channel.subscription.message).
func (c *Client) SubscribeEventSub(ctx context.Context, sessionID, broadcasterID string) error {
	for _, typ := range eventSubSubscriptions {
		body, err := json.Marshal(map[string]any{
			"type":    typ,
			"version": "1",
			"condition": map[string]string{
				"broadcaster_user_id": broadcasterID,
			},
			"transport": map[string]string{
				"method":     "websocket",
				"session_id": sessionID,
			},
		})
		if err != nil {
			return fmt.Errorf("twitch: eventsub subscribe: %w", err)
		}
		if _, err := c.postJSON(ctx, c.subscriptionsURL, body); err != nil {
			return fmt.Errorf("twitch: eventsub subscribe %s: %w", typ, err)
		}
	}
	return nil
}

func (c *Client) attempt(ctx context.Context, method, url string, body []byte, token string) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, fmt.Errorf("twitch: request %s: %w", url, err)
	}
	req.Header.Set("Client-Id", c.clientID)
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("twitch: %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errUnauthorized
	}
	// 202 Accepted — нормальный ответ POST eventsub/subscriptions
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("twitch: %s: статус %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
