// Тесты e04s01-t2 — внешний пакет (twitch_test): проверяем публичный API
// клиента (refresh-грант, автообновление при 401).
package twitch_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/EFGLab/efg-timer/internal/integrations/twitch"
)

// TestTokenRefresh — refresh-грант, автообновление при 401 и хранение
// обновлённой сессии в клиенте.
func TestTokenRefresh(t *testing.T) {
	t.Run("explicit refresh", func(t *testing.T) {
		var mu sync.Mutex
		var gotForm url.Values
		calls := 0
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			mu.Lock()
			gotForm = r.PostForm
			calls++
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"expires_in":    14000,
			})
		}))
		defer tokSrv.Close()

		client := twitch.NewClient(twitch.ClientOptions{
			ClientID: "test-client",
			Session:  twitch.TwitchSession{AccessToken: "old-access", RefreshToken: "old-refresh"},
			TokenURL: tokSrv.URL,
		})
		ns, err := client.Refresh(context.Background())
		if err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if ns.AccessToken != "new-access" || ns.RefreshToken != "new-refresh" {
			t.Fatalf("session = %+v", ns)
		}
		if got := client.Session(); got.AccessToken != "new-access" || got.RefreshToken != "new-refresh" {
			t.Fatalf("client session = %+v", got)
		}
		mu.Lock()
		defer mu.Unlock()
		if calls != 1 {
			t.Fatalf("calls = %d", calls)
		}
		for k, want := range map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "old-refresh",
			"client_id":     "test-client",
		} {
			if gotForm.Get(k) != want {
				t.Errorf("token %s = %q, want %q", k, gotForm.Get(k), want)
			}
		}
	})

	t.Run("auto refresh on 401", func(t *testing.T) {
		var apiMu sync.Mutex
		apiCalls := 0
		apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiMu.Lock()
			apiCalls++
			n := apiCalls
			apiMu.Unlock()
			if r.Header.Get("Client-Id") != "test-client" {
				t.Errorf("Client-Id = %q", r.Header.Get("Client-Id"))
			}
			auth := r.Header.Get("Authorization")
			if n == 1 {
				if auth != "Bearer old-access" {
					t.Errorf("запрос 1: Authorization = %q", auth)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "Unauthorized", "status": 401})
				return
			}
			if auth != "Bearer new-access" {
				t.Errorf("запрос %d: Authorization = %q", n, auth)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "123", "login": "coolstreamer", "display_name": "CoolStreamer"},
				},
			})
		}))
		defer apiSrv.Close()

		var tokMu sync.Mutex
		tokCalls := 0
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			tokMu.Lock()
			tokCalls++
			tokMu.Unlock()
			if r.PostForm.Get("grant_type") != "refresh_token" || r.PostForm.Get("refresh_token") != "old-refresh" {
				t.Errorf("token form = %v", r.PostForm)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
				"expires_in":    14000,
			})
		}))
		defer tokSrv.Close()

		var refreshed twitch.TwitchSession
		client := twitch.NewClient(twitch.ClientOptions{
			ClientID:  "test-client",
			Session:   twitch.TwitchSession{AccessToken: "old-access", RefreshToken: "old-refresh"},
			TokenURL:  tokSrv.URL,
			UsersURL:  apiSrv.URL,
			OnRefresh: func(s twitch.TwitchSession) { refreshed = s },
		})
		user, err := client.FetchUser(context.Background())
		if err != nil {
			t.Fatalf("FetchUser: %v", err)
		}
		if user.Login != "coolstreamer" || user.DisplayName != "CoolStreamer" || user.ID != "123" {
			t.Fatalf("user = %+v", user)
		}
		if got := client.Session(); got.AccessToken != "new-access" {
			t.Fatalf("client session после 401 = %+v", got)
		}
		if refreshed.AccessToken != "new-access" {
			t.Fatalf("OnRefresh не вызван: %+v", refreshed)
		}
		tokMu.Lock()
		defer tokMu.Unlock()
		if tokCalls != 1 {
			t.Fatalf("tokCalls = %d", tokCalls)
		}
	})

	t.Run("refresh failure propagates", func(t *testing.T) {
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":             "invalid_grant",
				"error_description": "refresh token is invalid",
			})
		}))
		defer tokSrv.Close()

		client := twitch.NewClient(twitch.ClientOptions{
			ClientID: "test-client",
			Session:  twitch.TwitchSession{AccessToken: "old-access", RefreshToken: "bad-refresh"},
			TokenURL: tokSrv.URL,
		})
		if _, err := client.Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid_grant") {
			t.Fatalf("ожидали invalid_grant, получили: %v", err)
		}
		if got := client.Session(); got.AccessToken != "old-access" {
			t.Fatalf("session изменился после ошибки: %+v", got)
		}
	})
}
