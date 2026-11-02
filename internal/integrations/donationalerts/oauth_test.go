package donationalerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestOAuth — e05s01-t1: authorization_code с client_secret (DA не
// поддерживает PKCE) через loopback-редирект и refresh-грант.
func TestOAuth(t *testing.T) {
	t.Run("authorize url", func(t *testing.T) {
		tokSrv := httptest.NewServer(http.NotFoundHandler())
		defer tokSrv.Close()

		// RedirectURL с портом 0: слушаем эфемерно, фактический порт
		// подставляется в redirect_uri (в проде — фиксированный 13579).
		flow, err := NewOAuthFlow(OAuthOptions{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     tokSrv.URL,
			RedirectURL:  "http://127.0.0.1:0/callback",
		})
		if err != nil {
			t.Fatalf("NewOAuthFlow: %v", err)
		}
		defer flow.Close()

		authURL, err := flow.Start()
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		u, err := url.Parse(authURL)
		if err != nil {
			t.Fatalf("authorize url: %v", err)
		}
		if got := u.Scheme + "://" + u.Host + u.Path; got != "https://www.donationalerts.com/oauth/authorize" {
			t.Fatalf("authorize endpoint = %q", got)
		}
		q := u.Query()
		for k, want := range map[string]string{
			"response_type": "code",
			"client_id":     "test-client",
			"scope":         "oauth-user-show oauth-donation-subscribe",
		} {
			if q.Get(k) != want {
				t.Errorf("authorize %s = %q, want %q", k, q.Get(k), want)
			}
		}
		if ru := q.Get("redirect_uri"); ru == "" || ru == "http://127.0.0.1:0/callback" {
			t.Errorf("redirect_uri = %q, want фактический порт вместо :0", ru)
		}
		if q.Get("state") == "" {
			t.Error("state пуст")
		}
		if q.Get("code_challenge") != "" {
			t.Error("у DA нет PKCE — code_challenge не должен отправляться")
		}
	})

	t.Run("success", func(t *testing.T) {
		var mu sync.Mutex
		var gotForm url.Values
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Errorf("token: parse form: %v", err)
			}
			mu.Lock()
			gotForm = r.PostForm
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "fake-access",
				"refresh_token": "fake-refresh",
				"expires_in":    14000,
			})
		}))
		defer tokSrv.Close()

		flow, err := NewOAuthFlow(OAuthOptions{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     tokSrv.URL,
			RedirectURL:  "http://127.0.0.1:0/callback",
		})
		if err != nil {
			t.Fatalf("NewOAuthFlow: %v", err)
		}
		defer flow.Close()

		authURL, err := flow.Start()
		if err != nil {
			t.Fatalf("Start: %v", err)
		}

		// «Браузер» пользователя: DA редиректит на loopback с code и state.
		u, err := url.Parse(authURL)
		if err != nil {
			t.Fatalf("authorize url: %v", err)
		}
		aq := u.Query()
		cb := aq.Get("redirect_uri") + "?code=test-code&state=" + aq.Get("state")
		resp, err := http.Get(cb)
		if err != nil {
			t.Fatalf("callback: %v", err)
		}
		resp.Body.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess, err := flow.Wait(ctx)
		if err != nil {
			t.Fatalf("Wait: %v", err)
		}
		if sess.AccessToken != "fake-access" || sess.RefreshToken != "fake-refresh" || sess.ExpiresIn != 14000 {
			t.Fatalf("session = %+v", sess)
		}

		mu.Lock()
		defer mu.Unlock()
		for k, want := range map[string]string{
			"grant_type":    "authorization_code",
			"code":          "test-code",
			"client_id":     "test-client",
			"client_secret": "test-secret",
			"redirect_uri":  aq.Get("redirect_uri"),
		} {
			if gotForm.Get(k) != want {
				t.Errorf("token %s = %q, want %q", k, gotForm.Get(k), want)
			}
		}
		if gotForm.Get("code_verifier") != "" {
			t.Error("у DA нет PKCE — code_verifier не должен отправляться")
		}
	})

	t.Run("state mismatch", func(t *testing.T) {
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("token endpoint не должен вызываться при чужом state")
		}))
		defer tokSrv.Close()

		flow, err := NewOAuthFlow(OAuthOptions{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			TokenURL:     tokSrv.URL,
			RedirectURL:  "http://127.0.0.1:0/callback",
		})
		if err != nil {
			t.Fatalf("NewOAuthFlow: %v", err)
		}
		defer flow.Close()

		authURL, err := flow.Start()
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		u, err := url.Parse(authURL)
		if err != nil {
			t.Fatalf("authorize url: %v", err)
		}
		cb := u.Query().Get("redirect_uri") + "?code=test-code&state=чужой-state"
		resp, err := http.Get(cb)
		if err != nil {
			t.Fatalf("callback: %v", err)
		}
		resp.Body.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := flow.Wait(ctx); err == nil || !strings.Contains(err.Error(), "state") {
			t.Fatalf("Wait err = %v, want ошибка state", err)
		}
	})

	t.Run("refresh", func(t *testing.T) {
		var mu sync.Mutex
		var gotForm url.Values
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Errorf("token: parse form: %v", err)
			}
			mu.Lock()
			gotForm = r.PostForm
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"expires_in":    14000,
			})
		}))
		defer tokSrv.Close()

		opts := OAuthOptions{ClientID: "test-client", ClientSecret: "test-secret", TokenURL: tokSrv.URL}
		sess := Session{AccessToken: "old-access", RefreshToken: "fake-refresh", ExpiresIn: 10}
		got, err := RefreshSession(context.Background(), opts, sess)
		if err != nil {
			t.Fatalf("RefreshSession: %v", err)
		}
		if got.AccessToken != "new-access" || got.RefreshToken != "new-refresh" {
			t.Fatalf("refresh session = %+v", got)
		}

		mu.Lock()
		defer mu.Unlock()
		for k, want := range map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "fake-refresh",
			"client_id":     "test-client",
			"client_secret": "test-secret",
		} {
			if gotForm.Get(k) != want {
				t.Errorf("refresh %s = %q, want %q", k, gotForm.Get(k), want)
			}
		}
	})

	t.Run("refresh error", func(t *testing.T) {
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":             "invalid_grant",
				"error_description": "refresh token expired",
			})
		}))
		defer tokSrv.Close()

		opts := OAuthOptions{ClientID: "test-client", ClientSecret: "test-secret", TokenURL: tokSrv.URL}
		sess := Session{AccessToken: "old", RefreshToken: "broken"}
		if _, err := RefreshSession(context.Background(), opts, sess); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
			t.Fatalf("RefreshSession err = %v, want invalid_grant", err)
		}
	})
}
