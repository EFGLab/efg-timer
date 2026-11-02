package twitch

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// TestOAuthFlow — e04s01-t1: authorization_code + PKCE через loopback-редирект.
func TestOAuthFlow(t *testing.T) {
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

		flow, err := NewOAuthFlow(OAuthOptions{ClientID: "test-client", TokenURL: tokSrv.URL})
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
		if got := u.Scheme + "://" + u.Host + u.Path; got != "https://id.twitch.tv/oauth2/authorize" {
			t.Fatalf("authorize endpoint = %q", got)
		}
		q := u.Query()
		for k, want := range map[string]string{
			"response_type":         "code",
			"client_id":             "test-client",
			"scope":                 "channel:read:subscriptions",
			"code_challenge_method": "S256",
		} {
			if q.Get(k) != want {
				t.Errorf("authorize %s = %q, want %q", k, q.Get(k), want)
			}
		}
		challenge := q.Get("code_challenge")
		if len(challenge) != 43 {
			t.Errorf("code_challenge длина = %d, want 43 (base64url sha256 без паддинга)", len(challenge))
		}
		if q.Get("state") == "" {
			t.Error("state пуст")
		}

		// «Браузер» пользователя: Twitch редиректит на loopback с code и state.
		cb := q.Get("redirect_uri") + "?code=test-code&state=" + q.Get("state")
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
		if sess.AccessToken != "fake-access" || sess.RefreshToken != "fake-refresh" {
			t.Fatalf("session = %+v", sess)
		}

		mu.Lock()
		defer mu.Unlock()
		for k, want := range map[string]string{
			"grant_type":   "authorization_code",
			"code":         "test-code",
			"client_id":    "test-client",
			"redirect_uri": q.Get("redirect_uri"),
		} {
			if gotForm.Get(k) != want {
				t.Errorf("token %s = %q, want %q", k, gotForm.Get(k), want)
			}
		}
		verifier := gotForm.Get("code_verifier")
		if n := len(verifier); n < 43 || n > 128 {
			t.Errorf("code_verifier длина = %d, want 43..128", n)
		}
		sum := sha256.Sum256([]byte(verifier))
		if ch := base64.RawURLEncoding.EncodeToString(sum[:]); ch != challenge {
			t.Errorf("PKCE: challenge верификатора %q != authorize %q", ch, challenge)
		}
	})

	t.Run("state mismatch", func(t *testing.T) {
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("token endpoint не должен вызываться при чужом state")
		}))
		defer tokSrv.Close()

		flow, err := NewOAuthFlow(OAuthOptions{ClientID: "test-client", TokenURL: tokSrv.URL})
		if err != nil {
			t.Fatalf("NewOAuthFlow: %v", err)
		}
		defer flow.Close()

		authURL, err := flow.Start()
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		u, _ := url.Parse(authURL)
		redirect := u.Query().Get("redirect_uri")
		resp, err := http.Get(redirect + "?code=x&state=attacker-state")
		if err != nil {
			t.Fatalf("callback: %v", err)
		}
		resp.Body.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := flow.Wait(ctx); err == nil {
			t.Fatal("ожидали ошибку при несовпадении state")
		}
	})
}
