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

const DefaultUserURL = "https://www.donationalerts.com/api/v1/user/oauth"

const DefaultCentrifugoSubscribeURL = "https://www.donationalerts.com/api/v1/centrifuge/subscribe"

type Client struct {
	opts ClientOptions
	mu   sync.Mutex
}

type ClientOptions struct {
	ClientID     string
	ClientSecret string
	Session      Session
	TokenURL     string
	UserURL      string
	SubscribeURL string
	HTTPClient   *http.Client
	OnRefresh    func(Session)
}

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

func (c *Client) Session() Session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opts.Session
}

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
