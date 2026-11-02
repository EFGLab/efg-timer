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
	AuthorizeEndpoint = "https://www.donationalerts.com/oauth/authorize"
	DefaultTokenURL   = "https://www.donationalerts.com/oauth/token"
	ScopeAlerts       = "oauth-user-show oauth-donation-subscribe"

	DefaultRedirectPort = 13579

	callbackPath = "/callback"
)

type OAuthOptions struct {
	ClientID     string
	ClientSecret string
	TokenURL     string
	RedirectURL  string
}

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

func (f *OAuthFlow) AuthorizeURL() string {
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", f.clientID)
	v.Set("redirect_uri", f.redirectURI)
	v.Set("scope", ScopeAlerts)
	v.Set("state", f.state)
	return AuthorizeEndpoint + "?" + v.Encode()
}

func (f *OAuthFlow) Start() (string, error) {
	go func() { _ = f.srv.Serve(f.ln) }()
	return f.AuthorizeURL(), nil
}

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
	default:
	}
}

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

func randomURLSafe(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
