package donationalerts_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/integrations/donationalerts"
)

// TestSocketToken — e05s01-t2: GET /api/v1/user/oauth с Bearer-токеном,
// авто-refresh при 401; socket_connection_token и user_id живут в памяти
// сессии (в JSON сессии не сериализуются).
func TestSocketToken(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var gotAuth string
		userSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/user/oauth" {
				t.Errorf("path = %q, want /api/v1/user/oauth", r.URL.Path)
			}
			gotAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                      12345,
				"name":                    "user1",
				"socket_connection_token": "sock-token",
			})
		}))
		defer userSrv.Close()

		cl := donationalerts.NewClient(donationalerts.ClientOptions{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			Session:      donationalerts.Session{AccessToken: "fake-access"},
			UserURL:      userSrv.URL + "/api/v1/user/oauth",
		})
		token, userID, err := cl.SocketToken(context.Background())
		if err != nil {
			t.Fatalf("SocketToken: %v", err)
		}
		if token != "sock-token" || userID != 12345 {
			t.Fatalf("got (%q, %d), want (sock-token, 12345)", token, userID)
		}
		if gotAuth != "Bearer fake-access" {
			t.Errorf("Authorization = %q, want Bearer fake-access", gotAuth)
		}
		if s := cl.Session(); s.SocketConnectionToken != "sock-token" {
			t.Errorf("socket token не сохранён в памяти сессии: %+v", s)
		}
	})

	t.Run("auto refresh on 401", func(t *testing.T) {
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

		var calls atomic.Int32
		var refreshed donationalerts.Session
		userSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer new-access" {
				t.Errorf("повторный запрос Authorization = %q, want Bearer new-access", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":                      777,
				"socket_connection_token": "sock2",
			})
		}))
		defer userSrv.Close()

		cl := donationalerts.NewClient(donationalerts.ClientOptions{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			Session:      donationalerts.Session{AccessToken: "old-access", RefreshToken: "old-refresh"},
			TokenURL:     tokSrv.URL,
			UserURL:      userSrv.URL + "/api/v1/user/oauth",
			OnRefresh: func(s donationalerts.Session) {
				mu.Lock()
				refreshed = s
				mu.Unlock()
			},
		})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, err := cl.SocketToken(ctx)
		if err != nil {
			t.Fatalf("SocketToken: %v", err)
		}
		if s := cl.Session(); s.AccessToken != "new-access" || s.RefreshToken != "new-refresh" {
			t.Fatalf("сессия после авто-refresh = %+v", s)
		}
		mu.Lock()
		defer mu.Unlock()
		if refreshed.AccessToken != "new-access" {
			t.Errorf("OnRefresh = %+v, want new-access", refreshed)
		}
		if gotForm.Get("grant_type") != "refresh_token" || gotForm.Get("refresh_token") != "old-refresh" ||
			gotForm.Get("client_id") != "test-client" || gotForm.Get("client_secret") != "test-secret" {
			t.Errorf("refresh form = %+v", gotForm)
		}
	})

	t.Run("refresh failure propagates", func(t *testing.T) {
		tokSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":             "invalid_grant",
				"error_description": "refresh token expired",
			})
		}))
		defer tokSrv.Close()

		userSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer userSrv.Close()

		cl := donationalerts.NewClient(donationalerts.ClientOptions{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			Session:      donationalerts.Session{AccessToken: "old", RefreshToken: "broken"},
			TokenURL:     tokSrv.URL,
			UserURL:      userSrv.URL + "/api/v1/user/oauth",
		})
		_, _, err := cl.SocketToken(context.Background())
		if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
			t.Fatalf("SocketToken err = %v, want invalid_grant", err)
		}
	})
}
