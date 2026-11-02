// Package donationalerts — интеграция DonationAlerts: OAuth authorization_code
// с client_secret (DA не поддерживает PKCE) через фиксированный
// loopback-редирект, refresh-грант, клиент API с socket_connection_token
// (e05s01-t2) и подписка на донаты (e05s02).
//
// Код зеркален internal/integrations/twitch (e04s01): вынесение общего
// OAuth-кита в общий пакет — отдельный рефакторинг вне скоупа v1.
package donationalerts

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
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// AuthorizeEndpoint — страница входа DonationAlerts (браузер пользователя
	// всегда попадает на настоящий адрес; фейковый нужен только для токена).
	AuthorizeEndpoint = "https://www.donationalerts.com/oauth/authorize"
	// DefaultTokenURL — обмен кода на токен и refresh-гранта.
	DefaultTokenURL = "https://www.donationalerts.com/oauth/token"
	// ScopeAlerts — скоупы: профиль и подписка на донат-события.
	ScopeAlerts = "oauth-user-show oauth-donation-subscribe"

	// DefaultRedirectPort — фиксированный порт loopback-редиректа: DA сверяет
	// redirect_uri байт-в-байт, поэтому пользователь заранее вписывает
	// http://localhost:13579/callback в настройках своего DA-приложения.
	DefaultRedirectPort = 13579

	callbackPath = "/callback"
)

// OAuthOptions — параметры OAuth-потока. TokenURL и RedirectURL
// переопределяются в тестах (фейковый эндпоинт, эфемерный порт).
type OAuthOptions struct {
	ClientID     string
	ClientSecret string
	TokenURL     string // пусто → DefaultTokenURL
	RedirectURL  string // пусто → http://localhost:13579/callback
}

// Session — результат входа; хранится в секции donationalerts конфига.
// SocketConnectionToken и UserID приходят из /api/v1/user/oauth (e05s01-t2)
// и живут только в памяти: краткоживущий socket-токен в конфиг не пишется,
// user_id дублируется в DonationalertsConfig.UserID (json:"-").
type Session struct {
	AccessToken           string `json:"access_token"`
	RefreshToken          string `json:"refresh_token"`
	ExpiresIn             int    `json:"expires_in"`
	SocketConnectionToken string `json:"-"`
	UserID                int64  `json:"-"`
}

type oauthCallback struct {
	code string
	err  error
}

// OAuthFlow — одноразовый поток входа: loopback-сервер (фиксированный порт
// из RedirectURL либо эфемерный, если порт :0), state против CSRF, обмен кода
// на токен с client_secret.
type OAuthFlow struct {
	clientID     string
	clientSecret string
	tokenURL     string
	redirectURI  string
	state        string
	ln           net.Listener
	srv          *http.Server
	resultCh     chan oauthCallback
	closeOnce    sync.Once
}

// NewOAuthFlow слушает 127.0.0.1 и готовит параметры входа.
func NewOAuthFlow(opts OAuthOptions) (*OAuthFlow, error) {
	if strings.TrimSpace(opts.ClientID) == "" || strings.TrimSpace(opts.ClientSecret) == "" {
		return nil, errors.New("donationalerts: нужны client_id и client_secret своего приложения с donationalerts.com")
	}
	redirect := opts.RedirectURL
	if redirect == "" {
		redirect = "http://localhost:" + strconv.Itoa(DefaultRedirectPort) + callbackPath
	}
	u, err := url.Parse(redirect)
	if err != nil || u.Scheme != "http" || u.Path != callbackPath {
		return nil, fmt.Errorf("donationalerts: redirect url %q: ожидается http://…%s", redirect, callbackPath)
	}
	var ln net.Listener
	if port := u.Port(); port == "" || port == "0" {
		// Эфемерный порт (тесты): фактический порт подставляется в redirect.
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("donationalerts: loopback listener: %w", err)
		}
		u.Host = u.Hostname() + ":" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
		redirect = u.String()
	} else {
		ln, err = net.Listen("tcp", "127.0.0.1:"+port)
		if err != nil {
			ln.Close()
			return nil, fmt.Errorf("donationalerts: порт %s занят — закройте другой экземпляр и повторите: %w", port, err)
		}
	}
	state, err := randomURLSafe(24)
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("donationalerts: state: %w", err)
	}
	f := &OAuthFlow{
		clientID:     opts.ClientID,
		clientSecret: opts.ClientSecret,
		tokenURL:     opts.TokenURL,
		redirectURI:  redirect,
		state:        state,
		ln:           ln,
		srv:          &http.Server{ReadHeaderTimeout: 5 * time.Second},
		resultCh:     make(chan oauthCallback, 1),
	}
	if f.tokenURL == "" {
		f.tokenURL = DefaultTokenURL
	}
	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, f.handleCallback)
	f.srv.Handler = mux
	return f, nil
}

// AuthorizeURL — ссылка для браузера: response_type=code, скоупы, state.
func (f *OAuthFlow) AuthorizeURL() string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", f.clientID)
	v.Set("redirect_uri", f.redirectURI)
	v.Set("scope", ScopeAlerts)
	v.Set("state", f.state)
	return AuthorizeEndpoint + "?" + v.Encode()
}

// Start запускает loopback-сервер и возвращает authorize-ссылку для браузера.
func (f *OAuthFlow) Start() (string, error) {
	go func() { _ = f.srv.Serve(f.ln) }()
	return f.AuthorizeURL(), nil
}

// Wait блокируется до callback'а DA, обменивает код на токен и закрывает
// loopback-сервер. Ошибка — при чужом state, отказе пользователя или
// неудачном обмене.
func (f *OAuthFlow) Wait(ctx context.Context) (Session, error) {
	select {
	case <-ctx.Done():
		f.Close()
		return Session{}, ctx.Err()
	case cb := <-f.resultCh:
		defer f.Close()
		if cb.err != nil {
			return Session{}, cb.err
		}
		v := url.Values{}
		v.Set("client_id", f.clientID)
		v.Set("client_secret", f.clientSecret)
		v.Set("code", cb.code)
		v.Set("grant_type", "authorization_code")
		v.Set("redirect_uri", f.redirectURI)
		sess, err := postToken(ctx, f.tokenURL, v)
		if err != nil {
			return Session{}, err
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
		f.push(oauthCallback{err: fmt.Errorf("donationalerts: отклонено: %s (%s)", e, q.Get("error_description"))})
		http.Error(w, "вход не удался", http.StatusBadRequest)
		return
	}
	if q.Get("state") != f.state {
		f.push(oauthCallback{err: errors.New("donationalerts: state не совпал (вероятный CSRF)")})
		http.Error(w, "state mismatch", http.StatusBadRequest)
		return
	}
	code := q.Get("code")
	if code == "" {
		f.push(oauthCallback{err: errors.New("donationalerts: DA не передал code")})
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

// RefreshSession обновляет пару токенов refresh-грантом; используется
// клиентом API при 401 (e05s01-t2) и main-ом после рестарта.
func RefreshSession(ctx context.Context, opts OAuthOptions, sess Session) (Session, error) {
	tokenURL := opts.TokenURL
	if tokenURL == "" {
		tokenURL = DefaultTokenURL
	}
	v := url.Values{}
	v.Set("client_id", opts.ClientID)
	v.Set("client_secret", opts.ClientSecret)
	v.Set("grant_type", "refresh_token")
	v.Set("refresh_token", sess.RefreshToken)
	return postToken(ctx, tokenURL, v)
}

// postToken — POST формы на токен-эндпоинт с разбором ответа/ошибки DA.
func postToken(ctx context.Context, tokenURL string, form url.Values) (Session, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return Session{}, fmt.Errorf("donationalerts: token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Session{}, fmt.Errorf("donationalerts: token endpoint: %w", err)
	}
	defer resp.Body.Close()
	var tr struct {
		Session
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return Session{}, fmt.Errorf("donationalerts: token ответ (%d): %w", resp.StatusCode, err)
	}
	if tr.Error != "" {
		return Session{}, fmt.Errorf("donationalerts: token: %s (%s)", tr.Error, tr.ErrorDescription)
	}
	if resp.StatusCode != http.StatusOK {
		return Session{}, fmt.Errorf("donationalerts: token endpoint: статус %d", resp.StatusCode)
	}
	if tr.AccessToken == "" {
		return Session{}, errors.New("donationalerts: token: пустой access_token")
	}
	return tr.Session, nil
}

// randomURLSafe — n байт криптослучайности в base64url без паддинга.
func randomURLSafe(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
