// Package twitch — интеграция Twitch: OAuth-вход (authorization code + PKCE
// через loopback-редирект), клиент Helix с автообновлением токена и
// EventSub WebSocket (e04s02).
package twitch

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// AuthorizeEndpoint — страница входа Twitch (браузер пользователя всегда
	// попадает на настоящий адрес; фейковый нужен только для token-эндпоинта).
	AuthorizeEndpoint = "https://id.twitch.tv/oauth2/authorize"
	// DefaultTokenURL — обмен кода на токен и refresh-гранта.
	DefaultTokenURL = "https://id.twitch.tv/oauth2/token"
	// ScopeSubscriptions — минимальный скоуп для подписок и ре-сабов (e04s02).
	ScopeSubscriptions = "channel:read:subscriptions"

	callbackPath = "/callback"
)

// OAuthOptions — параметры OAuth-потока. TokenURL переопределяется в тестах
// (фейковый token-эндпоинт).
type OAuthOptions struct {
	ClientID string
	TokenURL string // пусто → DefaultTokenURL
}

// TwitchSession — результат входа; хранится в секции twitch конфига.
type TwitchSession struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

type oauthCallback struct {
	code string
	err  error
}

// OAuthFlow — одноразовый поток входа: loopback-сервер на эфемерном порту,
// state против CSRF, PKCE S256, обмен кода на токен.
type OAuthFlow struct {
	clientID    string
	tokenURL    string
	redirectURI string
	state       string
	verifier    string
	ln          net.Listener
	srv         *http.Server
	resultCh    chan oauthCallback
	closeOnce   sync.Once
}

// NewOAuthFlow слушает 127.0.0.1 на эфемерном порту и готовит параметры входа.
func NewOAuthFlow(opts OAuthOptions) (*OAuthFlow, error) {
	if strings.TrimSpace(opts.ClientID) == "" {
		return nil, errors.New("twitch: пустой client_id")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("twitch: loopback listener: %w", err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		ln.Close()
		return nil, fmt.Errorf("twitch: не TCP-адрес: %T", ln.Addr())
	}
	verifier, err := randomURLSafe(64) // 86 символов base64url
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("twitch: code_verifier: %w", err)
	}
	state, err := randomURLSafe(24)
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("twitch: state: %w", err)
	}
	f := &OAuthFlow{
		clientID:    opts.ClientID,
		tokenURL:    opts.TokenURL,
		redirectURI: "http://localhost:" + strconv.Itoa(addr.Port) + callbackPath,
		state:       state,
		verifier:    verifier,
		ln:          ln,
		srv:         &http.Server{ReadHeaderTimeout: 5 * time.Second},
		resultCh:    make(chan oauthCallback, 1),
	}
	if f.tokenURL == "" {
		f.tokenURL = DefaultTokenURL
	}
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, f.handleCallback)
	f.srv.Handler = mux
	return f, nil
}

// AuthorizeURL — ссылка для браузера: response_type=code, PKCE S256, state.
func (f *OAuthFlow) AuthorizeURL() string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", f.clientID)
	v.Set("redirect_uri", f.redirectURI)
	v.Set("scope", ScopeSubscriptions)
	v.Set("state", f.state)
	v.Set("code_challenge", challengeS256(f.verifier))
	v.Set("code_challenge_method", "S256")
	return AuthorizeEndpoint + "?" + v.Encode()
}

// Start запускает loopback-сервер и возвращает authorize-ссылку для браузера.
func (f *OAuthFlow) Start() (string, error) {
	go func() { _ = f.srv.Serve(f.ln) }()
	return f.AuthorizeURL(), nil
}

// Wait блокируется до callback'а Twitch, обменивает код на токен и закрывает
// loopback-сервер. Ошибка — при чужом state, отказе пользователя или неудачном
// обмене.
func (f *OAuthFlow) Wait(ctx context.Context) (TwitchSession, error) {
	select {
	case <-ctx.Done():
		f.Close()
		return TwitchSession{}, ctx.Err()
	case cb := <-f.resultCh:
		defer f.Close()
		if cb.err != nil {
			return TwitchSession{}, cb.err
		}
		sess, err := f.exchange(ctx, cb.code)
		if err != nil {
			return TwitchSession{}, err
		}
		return sess, nil
	}
}

// Close останавливает loopback-сервер; повторный вызов безопасен.
func (f *OAuthFlow) Close() error {
	var err error
	f.closeOnce.Do(func() { err = f.srv.Close() })
	return err
}

func (f *OAuthFlow) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		f.push(oauthCallback{err: fmt.Errorf("twitch: отклонено: %s (%s)", e, q.Get("error_description"))})
		http.Error(w, "вход не удался", http.StatusBadRequest)
		return
	}
	if q.Get("state") != f.state {
		f.push(oauthCallback{err: errors.New("twitch: state не совпал (вероятный CSRF)")})
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	code := q.Get("code")
	if code == "" {
		f.push(oauthCallback{err: errors.New("twitch: Twitch не передал code")})
		http.Error(w, "code required", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "Вход выполнен — эту вкладку можно закрыть.")
	f.push(oauthCallback{code: code})
}

func (f *OAuthFlow) push(cb oauthCallback) {
	select {
	case f.resultCh <- cb:
	default: // повторный callback — игнорируем
	}
}

func (f *OAuthFlow) exchange(ctx context.Context, code string) (TwitchSession, error) {
	v := url.Values{}
	v.Set("client_id", f.clientID)
	v.Set("code", code)
	v.Set("code_verifier", f.verifier)
	v.Set("redirect_uri", f.redirectURI)
	v.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURL, strings.NewReader(v.Encode()))
	if err != nil {
		return TwitchSession{}, fmt.Errorf("twitch: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
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
		return TwitchSession{}, fmt.Errorf("twitch: token ответ (%d): %w", resp.StatusCode, err)
	}
	if tr.Error != "" {
		return TwitchSession{}, fmt.Errorf("twitch: token: %s (%s)", tr.Error, tr.ErrorDescription)
	}
	if resp.StatusCode != http.StatusOK {
		return TwitchSession{}, fmt.Errorf("twitch: token endpoint: статус %d", resp.StatusCode)
	}
	if tr.AccessToken == "" {
		return TwitchSession{}, errors.New("twitch: token: пустой access_token")
	}
	return tr.TwitchSession, nil
}

// randomURLSafe — n байт криптослучайности в base64url без паддинга
// (64 байта → 86 символов, внутри лимитов PKCE 43..128).
func randomURLSafe(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// challengeS256 — code_challenge = BASE64URL(SHA256(verifier)) без паддинга.
func challengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
