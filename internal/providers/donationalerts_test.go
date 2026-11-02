package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// daTestSecretStore — хранилище секретов в памяти (в приложении это OS credential store).
type daTestSecretStore struct {
	mu      sync.Mutex
	values  map[string][]byte
	saves   int
	deletes int
}

func newDATestSecretStore() *daTestSecretStore {
	return &daTestSecretStore{values: map[string][]byte{}}
}

func (s *daTestSecretStore) Load(key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (s *daTestSecretStore) Save(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	s.saves++
	return nil
}

func (s *daTestSecretStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	s.deletes++
	return nil
}

// deleteCount — сколько раз удаляли секрет.
func (s *daTestSecretStore) deleteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deletes
}

func daWriteJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// daCallbackURL — адрес, по которому браузер вернулся бы к loopback-серверу: хост 127.0.0.1
// (сервер слушает именно его), а в redirect_uri зашит localhost.
func daCallbackURL(t *testing.T, redirect, state, code string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("разбор redirect %q: %v", redirect, err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("разбор host:port %q: %v", u.Host, err)
	}
	u.Host = net.JoinHostPort("127.0.0.1", port)
	q := u.Query()
	q.Set("code", code)
	q.Set("state", state)
	u.RawQuery = q.Encode()
	return u.String()
}

// daIPv6CallbackURL — адрес callback'а на IPv6 ([::1]) с тем же портом, что и в redirect_uri.
func daIPv6CallbackURL(t *testing.T, redirect, state, code string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("разбор redirect %q: %v", redirect, err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("разбор host:port %q: %v", u.Host, err)
	}
	ipv6 := url.URL{
		Scheme: u.Scheme,
		Host:   net.JoinHostPort("::1", port),
		Path:   u.Path,
	}
	q := ipv6.Query()
	q.Set("code", code)
	q.Set("state", state)
	ipv6.RawQuery = q.Encode()
	return ipv6.String()
}

// daIPv6ProbeURL — адрес для проверки IPv6-слушателя: путь не callback'а, чтобы не сорвать вход.
func daIPv6ProbeURL(t *testing.T, redirect string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatalf("разбор redirect %q: %v", redirect, err)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("разбор host:port %q: %v", u.Host, err)
	}
	return "http://" + net.JoinHostPort("::1", port) + "/probe"
}

// daAuthResult — результат WaitAuth из горутины.
type daAuthResult struct {
	session DonationAlertsSession
	err     error
}

// TestDonationAlertsAuthFlow — полный вход: loopback, callback, обмен кода на токен, профиль, сохранение сессии.
func TestDonationAlertsAuthFlow(t *testing.T) {
	var mu sync.Mutex
	var tokenForm url.Values
	var userAuth string

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("token form: %v", err)
		}
		mu.Lock()
		tokenForm = r.PostForm
		mu.Unlock()
		daWriteJSON(t, w, http.StatusOK, map[string]any{
			"token_type":    "bearer",
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"expires_in":    3600,
		})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		userAuth = r.Header.Get("Authorization")
		mu.Unlock()
		// Ответ DA обёрнут в {"data":{…}} — id и code лежат внутри.
		daWriteJSON(t, w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"id":                      4242,
				"code":                    "donator",
				"socket_connection_token": "socket-1",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newDATestSecretStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
		HTTPClient:   srv.Client(),
		TokenURL:     srv.URL + "/token",
		UserURL:      srv.URL + "/user",
		ListenAddr:   "127.0.0.1:0",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	auth, err := p.BeginAuth(ctx)
	if err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}
	if auth.State == "" {
		t.Fatal("BeginAuth: пустой state")
	}
	if !p.IsRunning() {
		t.Fatal("IsRunning после BeginAuth = false")
	}

	// Ссылка для браузера: client_id, response_type=code, redirect_uri, scope и тот же state.
	parsed, err := url.Parse(auth.AuthorizeURL)
	if err != nil {
		t.Fatalf("разбор AuthorizeURL: %v", err)
	}
	q := parsed.Query()
	if !strings.HasPrefix(auth.AuthorizeURL, DonationAlertsAuthorizeURL) {
		t.Errorf("AuthorizeURL = %q, want префикс %q", auth.AuthorizeURL, DonationAlertsAuthorizeURL)
	}
	if q.Get("client_id") != "client-1" {
		t.Errorf("client_id = %q", q.Get("client_id"))
	}
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("redirect_uri") != auth.RedirectURL {
		t.Errorf("redirect_uri = %q, want %q", q.Get("redirect_uri"), auth.RedirectURL)
	}
	if q.Get("scope") != DonationAlertsScope {
		t.Errorf("scope = %q, want %q", q.Get("scope"), DonationAlertsScope)
	}
	if q.Get("state") != auth.State {
		t.Errorf("state в ссылке = %q, want %q", q.Get("state"), auth.State)
	}

	// Ждём callback в отдельной горутине: WaitAuth блокирует.
	resCh := make(chan daAuthResult, 1)
	go func() {
		session, err := p.WaitAuth(ctx, auth)
		resCh <- daAuthResult{session: session, err: err}
	}()

	callback := daCallbackURL(t, auth.RedirectURL, auth.State, "code-1")
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("WaitAuth: %v", res.err)
		}
		if res.session.AccessToken != "access-1" || res.session.RefreshToken != "refresh-1" {
			t.Fatalf("session = %+v", res.session)
		}
		if res.session.UserID != 4242 {
			t.Errorf("UserID = %d, want 4242", res.session.UserID)
		}
		if res.session.UserCode != "donator" {
			t.Errorf("UserCode = %q, want donator", res.session.UserCode)
		}
		if res.session.ExpiresAt.Before(time.Now()) {
			t.Errorf("ExpiresAt в прошлом: %v", res.session.ExpiresAt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitAuth не вернулся")
	}

	if p.IsRunning() {
		t.Error("IsRunning после входа = true")
	}

	// Сессия сохранена в хранилище (в settings.json токенов нет).
	stored, found, err := store.Load(DonationAlertsSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("сессия не сохранена: found=%v err=%v", found, err)
	}
	var persisted DonationAlertsSession
	if err := json.Unmarshal(stored, &persisted); err != nil {
		t.Fatalf("разбор сохранённой сессии: %v", err)
	}
	if persisted.AccessToken != "access-1" || persisted.RefreshToken != "refresh-1" {
		t.Errorf("сохранённая сессия = %+v", persisted)
	}
	if persisted.UserID != 4242 || persisted.UserCode != "donator" {
		t.Errorf("сохранённый профиль = %+v", persisted)
	}

	mem, ok := p.Session()
	if !ok || mem.AccessToken != "access-1" {
		t.Errorf("Session() = (%+v, %v)", mem, ok)
	}

	mu.Lock()
	defer mu.Unlock()
	if tokenForm.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", tokenForm.Get("grant_type"))
	}
	if tokenForm.Get("client_id") != "client-1" {
		t.Errorf("client_id = %q", tokenForm.Get("client_id"))
	}
	if tokenForm.Get("client_secret") != "secret-1" {
		t.Errorf("client_secret = %q", tokenForm.Get("client_secret"))
	}
	if tokenForm.Get("code") != "code-1" {
		t.Errorf("code = %q", tokenForm.Get("code"))
	}
	if tokenForm.Get("redirect_uri") != auth.RedirectURL {
		t.Errorf("redirect_uri = %q, want %q", tokenForm.Get("redirect_uri"), auth.RedirectURL)
	}
	if userAuth != "Bearer access-1" {
		t.Errorf("Authorization = %q", userAuth)
	}
}

// TestDonationAlertsStateMismatch — чужой state (CSRF) отклоняется.
func TestDonationAlertsStateMismatch(t *testing.T) {
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        newDATestSecretStore(),
		ListenAddr:   "127.0.0.1:0",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	auth, err := p.BeginAuth(ctx)
	if err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}

	resCh := make(chan daAuthResult, 1)
	go func() {
		session, err := p.WaitAuth(ctx, auth)
		resCh <- daAuthResult{session: session, err: err}
	}()

	callback := daCallbackURL(t, auth.RedirectURL, "чужая-state", "code-1")
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case res := <-resCh:
		if res.err == nil {
			t.Fatal("WaitAuth: want ошибка CSRF, got nil")
		}
		if !strings.Contains(res.err.Error(), "state") {
			t.Errorf("err = %v, want упоминание state", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitAuth не вернулся")
	}
}

// TestDonationAlertsTokenEndpointError — ошибка token-endpoint видна вызывающей стороне.
func TestDonationAlertsTokenEndpointError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		daWriteJSON(t, w, http.StatusInternalServerError, map[string]any{
			"error":             "server_error",
			"error_description": "бум",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        newDATestSecretStore(),
		HTTPClient:   srv.Client(),
		TokenURL:     srv.URL + "/token",
		ListenAddr:   "127.0.0.1:0",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	auth, err := p.BeginAuth(ctx)
	if err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}

	resCh := make(chan daAuthResult, 1)
	go func() {
		session, err := p.WaitAuth(ctx, auth)
		resCh <- daAuthResult{session: session, err: err}
	}()

	callback := daCallbackURL(t, auth.RedirectURL, auth.State, "code-1")
	resp, err := http.Get(callback)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case res := <-resCh:
		if res.err == nil {
			t.Fatal("WaitAuth: want ошибка token endpoint, got nil")
		}
		msg := res.err.Error()
		if !strings.Contains(msg, "500") {
			t.Errorf("err = %v, want статус 500", res.err)
		}
		if !strings.Contains(msg, "server_error") || !strings.Contains(msg, "бум") {
			t.Errorf("err = %v, want текст ответа DA", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitAuth не вернулся")
	}
}

// TestDonationAlertsWaitAuthContextCancel — отмена контекста срывает ожидание, без зависания.
func TestDonationAlertsWaitAuthContextCancel(t *testing.T) {
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        newDATestSecretStore(),
		ListenAddr:   "127.0.0.1:0",
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	auth, err := p.BeginAuth(ctx)
	if err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := p.WaitAuth(ctx, auth)
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitAuth не завершился после отмены контекста")
	}
	if p.IsRunning() {
		t.Error("IsRunning после отмены = true")
	}
}

// TestDonationAlertsCancelAuth — CancelAuth закрывает loopback-сервер и срывает ожидание.
func TestDonationAlertsCancelAuth(t *testing.T) {
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        newDATestSecretStore(),
		ListenAddr:   "127.0.0.1:0",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	auth, err := p.BeginAuth(ctx)
	if err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := p.WaitAuth(ctx, auth)
		errCh <- err
	}()

	time.Sleep(50 * time.Millisecond)
	p.CancelAuth()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitAuth не завершился после CancelAuth")
	}
	if p.IsRunning() {
		t.Error("IsRunning после CancelAuth = true")
	}
}

// TestDonationAlertsEnableRejected — общий Provider.Enable без UI запрещён.
func TestDonationAlertsEnableRejected(t *testing.T) {
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        newDATestSecretStore(),
	})
	token, err := p.Enable(context.Background())
	if err == nil || token != "" {
		t.Fatalf("Enable = (%q, %v), want пустой токен и ошибку", token, err)
	}
}

// TestDonationAlertsSessionStore — сохранение, чтение и удаление секрета.
func TestDonationAlertsSessionStore(t *testing.T) {
	store := newDATestSecretStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
	})

	ctx := context.Background()
	session := DonationAlertsSession{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
		UserID:       4242,
		UserCode:     "donator",
	}
	if err := p.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if _, found, err := store.Load(DonationAlertsSessionSecretKey); err != nil || !found {
		t.Fatalf("секрет не сохранён: found=%v err=%v", found, err)
	}

	// Неполную сессию сохранять нельзя.
	if err := p.SaveSession(ctx, DonationAlertsSession{AccessToken: "only-access"}); err == nil {
		t.Error("SaveSession без refresh-токена: want ошибка")
	}

	loaded, found, err := p.LoadSession(ctx)
	if err != nil || !found {
		t.Fatalf("LoadSession: found=%v err=%v", found, err)
	}
	if loaded.AccessToken != "access-1" || loaded.RefreshToken != "refresh-1" {
		t.Errorf("loaded = %+v", loaded)
	}
	if loaded.UserID != 4242 || loaded.UserCode != "donator" {
		t.Errorf("loaded профиль = %+v", loaded)
	}

	if err := p.DeleteSession(ctx); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, found, err := store.Load(DonationAlertsSessionSecretKey); err != nil || found {
		t.Fatalf("секрет не удалён: found=%v err=%v", found, err)
	}
	if _, ok := p.Session(); ok {
		t.Error("Session() после удаления: ok = true")
	}
}

// TestDonationAlertsRefreshSession — refresh-грант обновляет токены.
func TestDonationAlertsRefreshSession(t *testing.T) {
	var mu sync.Mutex
	var tokenForm url.Values

	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("token form: %v", err)
		}
		mu.Lock()
		tokenForm = r.PostForm
		mu.Unlock()
		daWriteJSON(t, w, http.StatusOK, map[string]any{
			"token_type":    "bearer",
			"access_token":  "access-2",
			"refresh_token": "refresh-2",
			"expires_in":    120,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newDATestSecretStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
		HTTPClient:   srv.Client(),
		TokenURL:     srv.URL + "/token",
	})

	ctx := context.Background()
	old := DonationAlertsSession{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(-time.Minute),
		UserID:       4242,
		UserCode:     "donator",
	}
	if err := p.SaveSession(ctx, old); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	updated, err := p.RefreshSession(ctx)
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if updated.AccessToken != "access-2" || updated.RefreshToken != "refresh-2" {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.ExpiresAt.Before(time.Now()) {
		t.Errorf("ExpiresAt в прошлом: %v", updated.ExpiresAt)
	}
	// Профиль при refresh не меняется.
	if updated.UserID != 4242 || updated.UserCode != "donator" {
		t.Errorf("updated профиль = %+v", updated)
	}

	stored, found, err := store.Load(DonationAlertsSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("обновлённая сессия не сохранена: found=%v err=%v", found, err)
	}
	var persisted DonationAlertsSession
	if err := json.Unmarshal(stored, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.AccessToken != "access-2" {
		t.Errorf("сохранён access = %q, want access-2", persisted.AccessToken)
	}

	mu.Lock()
	defer mu.Unlock()
	if tokenForm.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q", tokenForm.Get("grant_type"))
	}
	if tokenForm.Get("refresh_token") != "refresh-1" {
		t.Errorf("refresh_token = %q", tokenForm.Get("refresh_token"))
	}
	if tokenForm.Get("client_id") != "client-1" {
		t.Errorf("client_id = %q", tokenForm.Get("client_id"))
	}
	if tokenForm.Get("client_secret") != "secret-1" {
		t.Errorf("client_secret = %q", tokenForm.Get("client_secret"))
	}
	if tokenForm.Get("scope") != DonationAlertsScope {
		t.Errorf("scope = %q, want %q", tokenForm.Get("scope"), DonationAlertsScope)
	}
}

// TestDonationAlertsLogoutRemovesSecret — Logout удаляет секрет и не ходит в revoke (у DA он не документирован).
func TestDonationAlertsLogoutRemovesSecret(t *testing.T) {
	var requests int
	var mu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newDATestSecretStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
		HTTPClient:   srv.Client(),
		TokenURL:     srv.URL + "/token",
		UserURL:      srv.URL + "/user",
	})

	ctx := context.Background()
	session := DonationAlertsSession{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	}
	if err := p.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	if err := p.Logout(ctx); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, found, err := store.Load(DonationAlertsSessionSecretKey); err != nil || found {
		t.Fatalf("секрет не удалён: found=%v err=%v", found, err)
	}
	if _, ok := p.Session(); ok {
		t.Error("Session() после Logout: ok = true")
	}

	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Errorf("Logout сделал %d сетевых запросов, revoke у DA не документирован", requests)
	}
}

// TestDonationAlertsBeginAuthRequiresCredentials — без client_id или client_secret вход невозможен.
func TestDonationAlertsBeginAuthRequiresCredentials(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts DonationAlertsOptions
	}{
		{name: "нет client_id", opts: DonationAlertsOptions{ClientSecret: "secret-1"}},
		{name: "нет client_secret", opts: DonationAlertsOptions{ClientID: "client-1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := NewDonationAlertsProvider(tc.opts)
			if _, err := p.BeginAuth(context.Background()); err == nil {
				t.Fatal("BeginAuth: want ошибка, got nil")
			}
		})
	}
}

// TestDonationAlertsDoubleBeginAuth — второй вход, пока идёт первый, не поднимается.
func TestDonationAlertsDoubleBeginAuth(t *testing.T) {
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        newDATestSecretStore(),
		ListenAddr:   "127.0.0.1:0",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := p.BeginAuth(ctx); err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}
	if _, err := p.BeginAuth(ctx); err == nil {
		t.Fatal("повторный BeginAuth: want ошибка, got nil")
	}
	p.CancelAuth()
}

// TestDonationAlertsAuthFlowIPv6 — callback, пришедший на [::1], обрабатывается так же, как на IPv4.
func TestDonationAlertsAuthFlowIPv6(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		daWriteJSON(t, w, http.StatusOK, map[string]any{
			"token_type":    "bearer",
			"access_token":  "access-6",
			"refresh_token": "refresh-6",
			"expires_in":    3600,
		})
	})
	mux.HandleFunc("/user", func(w http.ResponseWriter, r *http.Request) {
		daWriteJSON(t, w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"id":                      77,
				"code":                    "donator6",
				"socket_connection_token": "socket-6",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newDATestSecretStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
		HTTPClient:   srv.Client(),
		TokenURL:     srv.URL + "/token",
		UserURL:      srv.URL + "/user",
		ListenAddr:   "127.0.0.1:0",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	auth, err := p.BeginAuth(ctx)
	if err != nil {
		t.Fatalf("BeginAuth: %v", err)
	}

	// Проба IPv6-слушателя: путь не callback'а, чтобы не сорвать ожидание.
	probe := &http.Client{Timeout: 2 * time.Second}
	probeResp, probeErr := probe.Get(daIPv6ProbeURL(t, auth.RedirectURL))
	if probeErr != nil {
		t.Skipf("IPv6 loopback недоступен: %v", probeErr)
	}
	_ = probeResp.Body.Close()

	resCh := make(chan daAuthResult, 1)
	go func() {
		session, err := p.WaitAuth(ctx, auth)
		resCh <- daAuthResult{session: session, err: err}
	}()

	callback := daIPv6CallbackURL(t, auth.RedirectURL, auth.State, "code-6")
	resp, err := probe.Get(callback)
	if err != nil {
		t.Fatalf("callback по IPv6: %v", err)
	}
	_ = resp.Body.Close()

	select {
	case res := <-resCh:
		if res.err != nil {
			t.Fatalf("WaitAuth: %v", res.err)
		}
		if res.session.AccessToken != "access-6" {
			t.Errorf("access = %q, want access-6", res.session.AccessToken)
		}
		if res.session.UserID != 77 || res.session.UserCode != "donator6" {
			t.Errorf("профиль = %+v", res.session)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitAuth не вернулся")
	}
}

// TestDonationAlertsLogoutAtKeepsNewSession — Logout по снимку эпохи не стирает новую сессию.
func TestDonationAlertsLogoutAtKeepsNewSession(t *testing.T) {
	store := newDATestSecretStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
	})

	ctx := context.Background()
	old := DonationAlertsSession{AccessToken: "old-access", RefreshToken: "old-refresh"}
	epoch := p.SessionEpoch()
	fresh := DonationAlertsSession{AccessToken: "new-access", RefreshToken: "new-refresh"}
	if err := p.SaveSession(ctx, fresh); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	if p.SessionEpoch() == epoch {
		t.Fatal("эпоха не выросла после SaveSession")
	}

	// Logout по старой эпохе: пользователь успел начать новый вход, новую сессию стирать нельзя.
	if err := p.LogoutAt(ctx, old, true, epoch); err != nil {
		t.Fatalf("LogoutAt (старая эпоха): %v", err)
	}
	loaded, found, err := store.Load(DonationAlertsSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("новая сессия удалена: found=%v err=%v", found, err)
	}
	var got DonationAlertsSession
	if err := json.Unmarshal(loaded, &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new-access" {
		t.Fatalf("access = %q, want new-access", got.AccessToken)
	}

	// Logout по текущей эпохе: удаляем.
	if err := p.LogoutAt(ctx, got, true, p.SessionEpoch()); err != nil {
		t.Fatalf("LogoutAt (текущая эпоха): %v", err)
	}
	if _, found, err := store.Load(DonationAlertsSessionSecretKey); err != nil || found {
		t.Fatalf("секрет не удалён: found=%v err=%v", found, err)
	}
	if _, ok := p.Session(); ok {
		t.Error("Session() после LogoutAt: ok = true")
	}
}

// TestDonationAlertsLogoutKeepsSessionSavedAfterSnapshot — Logout берёт эпоху в момент вызова.
func TestDonationAlertsLogoutKeepsSessionSavedAfterSnapshot(t *testing.T) {
	store := newDATestSecretStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
	})

	ctx := context.Background()
	old := DonationAlertsSession{AccessToken: "old-access", RefreshToken: "old-refresh"}
	if err := p.SaveSession(ctx, old); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	// Снимок эпохи, как его снимает слой приложения при выключении.
	epoch := p.SessionEpoch()

	fresh := DonationAlertsSession{AccessToken: "new-access", RefreshToken: "new-refresh"}
	if err := p.SaveSession(ctx, fresh); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	if err := p.LogoutAt(ctx, old, true, epoch); err != nil {
		t.Fatalf("LogoutAt: %v", err)
	}
	loaded, found, err := store.Load(DonationAlertsSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("новая сессия удалена: found=%v err=%v", found, err)
	}
	var got DonationAlertsSession
	if err := json.Unmarshal(loaded, &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new-access" {
		t.Fatalf("access = %q, want new-access", got.AccessToken)
	}
}

// TestDonationAlertsDeleteSessionBumpsEpoch — DeleteSession двигает эпоху, поэтому устаревший Logout
// по старой эпохе не стирает свежую сессию.
func TestDonationAlertsDeleteSessionBumpsEpoch(t *testing.T) {
	store := newDATestSecretStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
	})

	ctx := context.Background()
	old := DonationAlertsSession{AccessToken: "old-access", RefreshToken: "old-refresh"}
	if err := p.SaveSession(ctx, old); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	epoch := p.SessionEpoch()

	if err := p.DeleteSession(ctx); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if p.SessionEpoch() == epoch {
		t.Fatal("эпоха не выросла после DeleteSession")
	}

	// Пользователь снова вошёл: свежая сессия на месте.
	fresh := DonationAlertsSession{AccessToken: "new-access", RefreshToken: "new-refresh"}
	if err := p.SaveSession(ctx, fresh); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Отложенный выход по устаревшей эпохе не должен ничего стирать.
	deletes := store.deleteCount()
	if err := p.LogoutAt(ctx, old, true, epoch); err != nil {
		t.Fatalf("LogoutAt: %v", err)
	}
	if store.deleteCount() != deletes {
		t.Errorf("устаревший LogoutAt удалил секрет (удалений: %d → %d)", deletes, store.deleteCount())
	}

	loaded, found, err := store.Load(DonationAlertsSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("свежая сессия удалена: found=%v err=%v", found, err)
	}
	var got DonationAlertsSession
	if err := json.Unmarshal(loaded, &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new-access" {
		t.Fatalf("access = %q, want new-access", got.AccessToken)
	}
}

// daGateStore — хранилище, в котором удаление ждёт разрешения: гонку удаления и сохранения можно развести.
type daGateStore struct {
	mu      sync.Mutex
	values  map[string][]byte
	entered chan struct{} // удаление началось
	release chan struct{} // удаление можно завершить
	once    sync.Once
}

func newDAGateStore() *daGateStore {
	return &daGateStore{
		values:  map[string][]byte{},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (s *daGateStore) Load(key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (s *daGateStore) Save(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *daGateStore) Delete(key string) error {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	return nil
}

// TestDonationAlertsDeleteSessionRaceWithSave — сессия, сохранённая во время удаления, остаётся на месте.
func TestDonationAlertsDeleteSessionRaceWithSave(t *testing.T) {
	store := newDAGateStore()
	p := NewDonationAlertsProvider(DonationAlertsOptions{
		ClientID:     "client-1",
		ClientSecret: "secret-1",
		Store:        store,
	})

	ctx := context.Background()
	if err := p.SaveSession(ctx, DonationAlertsSession{AccessToken: "old-access", RefreshToken: "old-refresh"}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Удаление стартует и застревает в хранилище.
	deleteDone := make(chan error, 1)
	go func() { deleteDone <- p.DeleteSession(ctx) }()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("удаление не началось")
	}

	// Пока удаление ждёт, сохраняется новая сессия.
	saveDone := make(chan error, 1)
	go func() {
		saveDone <- p.SaveSession(ctx, DonationAlertsSession{AccessToken: "new-access", RefreshToken: "new-refresh"})
	}()
	time.Sleep(100 * time.Millisecond)
	close(store.release)

	if err := <-deleteDone; err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if err := <-saveDone; err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	loaded, found, err := store.Load(DonationAlertsSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("свежая сессия удалена: found=%v err=%v", found, err)
	}
	var got DonationAlertsSession
	if err := json.Unmarshal(loaded, &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new-access" {
		t.Fatalf("access = %q, want new-access", got.AccessToken)
	}
	mem, ok := p.Session()
	if !ok || mem.AccessToken != "new-access" {
		t.Fatalf("Session() = (%+v, %v), want new-access", mem, ok)
	}
}
