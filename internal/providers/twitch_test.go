package providers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type memorySecretStore struct {
	mu      sync.Mutex
	values  map[string][]byte
	deletes int
}

func newMemorySecretStore() *memorySecretStore {
	return &memorySecretStore{values: map[string][]byte{}}
}

func (s *memorySecretStore) Load(key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[key]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), v...), true, nil
}

func (s *memorySecretStore) Save(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *memorySecretStore) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, key)
	s.deletes++
	return nil
}

// deleteCount — сколько раз удаляли секрет.
func (s *memorySecretStore) deleteCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deletes
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode: %v", err)
	}
}

// TestTwitchDeviceFlow — DCF: device → pending → token → helix/users, плюс сохранение сессии.
func TestTwitchDeviceFlow(t *testing.T) {
	var mu sync.Mutex
	var deviceForm, tokenForm, userHdr url.Values
	var tokenCalls int

	mux := http.NewServeMux()
	mux.HandleFunc("/device", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("device form: %v", err)
		}
		mu.Lock()
		deviceForm = r.PostForm
		mu.Unlock()
		writeJSON(t, w, http.StatusOK, map[string]any{
			"device_code":      "dev-code",
			"expires_in":       1800,
			"interval":         5,
			"user_code":        "ABCDEFGH",
			"verification_uri": "https://www.twitch.tv/activate?public=true&device-code=ABCDEFGH",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("token form: %v", err)
		}
		mu.Lock()
		tokenForm = r.PostForm
		tokenCalls++
		n := tokenCalls
		mu.Unlock()
		if n == 1 {
			writeJSON(t, w, http.StatusBadRequest, map[string]any{
				"status": 400, "message": "authorization_pending",
			})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token":  "access-1",
			"refresh_token": "refresh-1",
			"expires_in":    14400,
			"scope":         []string{TwitchScopeSubscriptions},
			"token_type":    "bearer",
		})
	})
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		userHdr = url.Values{
			"client-id":     {r.Header.Get("Client-Id")},
			"authorization": {r.Header.Get("Authorization")},
		}
		mu.Unlock()
		writeJSON(t, w, http.StatusOK, map[string]any{
			"data": []map[string]any{{
				"id": "42", "login": "streamer", "display_name": "Streamer",
			}},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newMemorySecretStore()
	p := NewTwitchProvider(TwitchOptions{
		ClientID:     "client-1",
		Store:        store,
		HTTPClient:   srv.Client(),
		DeviceURL:    srv.URL + "/device",
		TokenURL:     srv.URL + "/token",
		UsersURL:     srv.URL + "/users",
		RevokeURL:    srv.URL + "/revoke",
		PollInterval: time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	auth, err := p.BeginDeviceAuth(ctx)
	if err != nil {
		t.Fatalf("BeginDeviceAuth: %v", err)
	}
	if auth.UserCode != "ABCDEFGH" || auth.DeviceCode != "dev-code" {
		t.Fatalf("auth = %+v", auth)
	}

	session, err := p.PollDeviceAuth(ctx, auth)
	if err != nil {
		t.Fatalf("PollDeviceAuth: %v", err)
	}
	if session.AccessToken != "access-1" || session.RefreshToken != "refresh-1" {
		t.Fatalf("session = %+v", session)
	}
	if session.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expiry в прошлом: %v", session.ExpiresAt)
	}

	if err := p.SaveSession(ctx, session); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	loaded, found, err := p.LoadSession(ctx)
	if err != nil || !found {
		t.Fatalf("LoadSession: found=%v err=%v", found, err)
	}
	if loaded.AccessToken != session.AccessToken || loaded.RefreshToken != session.RefreshToken {
		t.Fatalf("loaded = %+v, want %+v", loaded, session)
	}

	user, updated, err := p.FetchUser(ctx, session)
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if user.Login != "streamer" || user.ID != "42" {
		t.Fatalf("user = %+v", user)
	}
	if updated.AccessToken != session.AccessToken {
		t.Fatalf("FetchUser обновил токен без 401: %+v", updated)
	}

	mu.Lock()
	defer mu.Unlock()
	if deviceForm.Get("client_id") != "client-1" {
		t.Errorf("device client_id = %q", deviceForm.Get("client_id"))
	}
	if deviceForm.Get("scopes") != TwitchScopeSubscriptions {
		t.Errorf("device scopes = %q", deviceForm.Get("scopes"))
	}
	if tokenForm.Get("device_code") != "dev-code" {
		t.Errorf("token device_code = %q", tokenForm.Get("device_code"))
	}
	if tokenForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
		t.Errorf("token grant_type = %q", tokenForm.Get("grant_type"))
	}
	if tokenForm.Get("client_secret") != "" {
		t.Errorf("token client_secret не должен отправляться")
	}
	if userHdr.Get("client-id") != "client-1" {
		t.Errorf("helix Client-Id = %q", userHdr.Get("client-id"))
	}
	if userHdr.Get("authorization") != "Bearer access-1" {
		t.Errorf("helix Authorization = %q", userHdr.Get("authorization"))
	}
}

// TestTwitchFetchUserRefreshesOn401 — токен обновляется при 401, провайдер сохраняет новую сессию.
func TestTwitchFetchUserRefreshesOn401(t *testing.T) {
	var mu sync.Mutex
	var tokenForm url.Values
	var userCalls int

	mux := http.NewServeMux()
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		userCalls++
		n := userCalls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"data": []map[string]any{{
				"id": "42", "login": "streamer", "display_name": "Streamer",
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("token form: %v", err)
		}
		mu.Lock()
		tokenForm = r.PostForm
		mu.Unlock()
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token":  "access-2",
			"refresh_token": "refresh-2",
			"expires_in":    14400,
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newMemorySecretStore()
	p := NewTwitchProvider(TwitchOptions{
		ClientID:   "client-1",
		Store:      store,
		HTTPClient: srv.Client(),
		TokenURL:   srv.URL + "/token",
		UsersURL:   srv.URL + "/users",
	})

	old := TwitchSession{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	user, updated, err := p.FetchUser(context.Background(), old)
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if user.Login != "streamer" {
		t.Fatalf("user = %+v", user)
	}
	if updated.AccessToken != "access-2" || updated.RefreshToken != "refresh-2" {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.ExpiresAt.Before(time.Now()) {
		t.Fatalf("новая expiry в прошлом: %v", updated.ExpiresAt)
	}

	stored, found, loadErr := store.Load(TwitchSessionSecretKey)
	if loadErr != nil || !found {
		t.Fatalf("обновлённая сессия не сохранена: found=%v err=%v", found, loadErr)
	}
	var persisted TwitchSession
	if err := json.Unmarshal(stored, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.RefreshToken != "refresh-2" {
		t.Fatalf("сохранён refresh = %q", persisted.RefreshToken)
	}

	mu.Lock()
	defer mu.Unlock()
	if tokenForm.Get("grant_type") != "refresh_token" {
		t.Errorf("grant_type = %q", tokenForm.Get("grant_type"))
	}
	if tokenForm.Get("refresh_token") != "refresh-old" {
		t.Errorf("refresh_token = %q", tokenForm.Get("refresh_token"))
	}
	if tokenForm.Get("client_secret") != "" {
		t.Errorf("refresh не должен отправлять client_secret")
	}
}

// TestTwitchLogoutRevokesAndDeletes — revoke best effort: сначала access, при ошибке — refresh.
func TestTwitchLogoutRevokesAndDeletes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		revokeCode int
		wantErr    bool
		wantTokens []string
	}{
		{name: "success", revokeCode: http.StatusOK, wantTokens: []string{"access-1"}},
		{
			name:       "revoke error falls back to refresh and still deletes",
			revokeCode: http.StatusInternalServerError,
			wantErr:    true,
			wantTokens: []string{"access-1", "refresh-1"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var revokeForms []url.Values

			mux := http.NewServeMux()
			mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Errorf("revoke form: %v", err)
				}
				mu.Lock()
				revokeForms = append(revokeForms, r.PostForm)
				mu.Unlock()
				w.WriteHeader(tc.revokeCode)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			store := newMemorySecretStore()
			session := TwitchSession{
				AccessToken:  "access-1",
				RefreshToken: "refresh-1",
				ExpiresAt:    time.Now().Add(time.Hour),
			}
			data, err := json.Marshal(session)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(TwitchSessionSecretKey, data); err != nil {
				t.Fatal(err)
			}

			p := NewTwitchProvider(TwitchOptions{
				ClientID:   "client-1",
				Store:      store,
				HTTPClient: srv.Client(),
				RevokeURL:  srv.URL + "/revoke",
			})
			err = p.Logout(context.Background())
			if tc.wantErr && err == nil {
				t.Fatal("Logout: want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Logout: %v", err)
			}

			if _, found, err := store.Load(TwitchSessionSecretKey); err != nil || found {
				t.Fatalf("сессия не удалена: found=%v err=%v", found, err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(revokeForms) != len(tc.wantTokens) {
				t.Fatalf("revoke вызовов = %d, want %d", len(revokeForms), len(tc.wantTokens))
			}
			for i, want := range tc.wantTokens {
				if got := revokeForms[i].Get("token"); got != want {
					t.Errorf("revoke[%d] token = %q, want %q", i, got, want)
				}
				if got := revokeForms[i].Get("client_id"); got != "client-1" {
					t.Errorf("revoke[%d] client_id = %q", i, got)
				}
				if revokeForms[i].Get("client_secret") != "" {
					t.Errorf("revoke[%d] не должен отправлять client_secret", i)
				}
			}
		})
	}
}

// TestTwitchLogoutDoesNotDeleteNewSession — старый Logout не стирает сессию после повторного входа.
func TestTwitchLogoutDoesNotDeleteNewSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newMemorySecretStore()
	p := NewTwitchProvider(TwitchOptions{
		ClientID:   "client-1",
		Store:      store,
		HTTPClient: srv.Client(),
		RevokeURL:  srv.URL + "/revoke",
	})

	old := TwitchSession{AccessToken: "old-access", RefreshToken: "old-refresh"}
	epoch := p.SessionEpoch()
	fresh := TwitchSession{AccessToken: "new-access", RefreshToken: "new-refresh"}
	if err := p.SaveSession(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}

	if err := p.LogoutAt(context.Background(), old, true, epoch); err != nil {
		t.Fatalf("LogoutAt: %v", err)
	}
	loaded, found, err := store.Load(TwitchSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("новая сессия удалена: found=%v err=%v", found, err)
	}
	var got TwitchSession
	if err := json.Unmarshal(loaded, &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new-access" {
		t.Fatalf("access = %q, want new-access", got.AccessToken)
	}
}

// TestTwitchPollErrorBranches — access_denied и expired_token завершают вход ошибкой.
func TestTwitchPollErrorBranches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		code    string
		wantErr string
	}{
		{name: "access_denied", code: "access_denied", wantErr: "вход отклонён"},
		{name: "expired_token", code: "expired_token", wantErr: "срок действия кода истёк"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusBadRequest, map[string]any{"status": 400, "message": tc.code})
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			p := NewTwitchProvider(TwitchOptions{
				ClientID:     "client-1",
				Store:        newMemorySecretStore(),
				HTTPClient:   srv.Client(),
				TokenURL:     srv.URL + "/token",
				PollInterval: time.Millisecond,
			})

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			_, err := p.PollDeviceAuth(ctx, TwitchDeviceAuth{
				DeviceCode: "dev-code",
				ExpiresIn:  time.Minute,
				Interval:   time.Millisecond,
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want содержит %q", err, tc.wantErr)
			}
		})
	}
}

// TestTwitchPollSlowDownIncreasesInterval — slow_down не фатален: интервал растёт (+5с по RFC 8628).
func TestTwitchPollSlowDownIncreasesInterval(t *testing.T) {
	var mu sync.Mutex
	var calls int
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		writeJSON(t, w, http.StatusBadRequest, map[string]any{"status": 400, "message": "slow_down"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewTwitchProvider(TwitchOptions{
		ClientID:     "client-1",
		Store:        newMemorySecretStore(),
		HTTPClient:   srv.Client(),
		TokenURL:     srv.URL + "/token",
		PollInterval: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := p.PollDeviceAuth(ctx, TwitchDeviceAuth{
			DeviceCode: "dev-code",
			ExpiresIn:  time.Minute,
			Interval:   time.Millisecond,
		})
		errCh <- err
	}()

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Fatalf("запросов после slow_down = %d, want 1 (интервал не вырос)", n)
	}

	p.CancelAuth()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PollDeviceAuth не завершился после отмены")
	}
}

// TestTwitchFetchUserKeepsRotatedSessionOnProfileError — ротированная сессия переживает ошибку профиля.
func TestTwitchFetchUserKeepsRotatedSessionOnProfileError(t *testing.T) {
	var mu sync.Mutex
	var userCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		userCalls++
		n := userCalls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token":  "access-2",
			"refresh_token": "refresh-2",
			"expires_in":    14400,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newMemorySecretStore()
	p := NewTwitchProvider(TwitchOptions{
		ClientID:   "client-1",
		Store:      store,
		HTTPClient: srv.Client(),
		TokenURL:   srv.URL + "/token",
		UsersURL:   srv.URL + "/users",
	})

	old := TwitchSession{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	if _, _, err := p.FetchUser(context.Background(), old); err == nil {
		t.Fatal("FetchUser: want error, got nil")
	}

	stored, found, err := store.Load(TwitchSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("ротированная сессия не сохранена: found=%v err=%v", found, err)
	}
	var persisted TwitchSession
	if err := json.Unmarshal(stored, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.AccessToken != "access-2" || persisted.RefreshToken != "refresh-2" {
		t.Fatalf("persisted = %+v, want access-2/refresh-2", persisted)
	}
}

// TestTwitchEnableRejectedWithoutUI — общий Provider.Enable для device flow запрещён.
func TestTwitchEnableRejectedWithoutUI(t *testing.T) {
	p := NewTwitchProvider(TwitchOptions{
		ClientID: "client-1",
		Store:    newMemorySecretStore(),
	})
	token, err := p.Enable(context.Background())
	if err == nil || token != "" {
		t.Fatalf("Enable = (%q, %v), want пустой токен и ошибку", token, err)
	}
}

// TestTwitchFetchUserWithoutExpiresInKeepsSessionValid — refresh без expires_in не делает сессию протухшей:
// ExpiresIn legacy-клиента — остаток прежнего снимка, нулевое значение брать нельзя.
func TestTwitchFetchUserWithoutExpiresInKeepsSessionValid(t *testing.T) {
	var mu sync.Mutex
	var userCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/users", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		userCalls++
		n := userCalls
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"data": []map[string]any{{
				"id": "42", "login": "streamer", "display_name": "Streamer",
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		// Ответ без expires_in: клиент не знает срок жизни нового токена.
		writeJSON(t, w, http.StatusOK, map[string]any{
			"access_token":  "access-2",
			"refresh_token": "refresh-2",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newMemorySecretStore()
	p := NewTwitchProvider(TwitchOptions{
		ClientID:   "client-1",
		Store:      store,
		HTTPClient: srv.Client(),
		TokenURL:   srv.URL + "/token",
		UsersURL:   srv.URL + "/users",
	})

	old := TwitchSession{
		AccessToken:  "access-old",
		RefreshToken: "refresh-old",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	_, updated, err := p.FetchUser(context.Background(), old)
	if err != nil {
		t.Fatalf("FetchUser: %v", err)
	}
	if updated.AccessToken != "access-2" {
		t.Fatalf("updated = %+v, want access-2", updated)
	}
	if !updated.ExpiresAt.After(time.Now()) {
		t.Fatalf("ExpiresAt в прошлом: %v", updated.ExpiresAt)
	}

	stored, found, err := store.Load(TwitchSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("обновлённая сессия не сохранена: found=%v err=%v", found, err)
	}
	var persisted TwitchSession
	if err := json.Unmarshal(stored, &persisted); err != nil {
		t.Fatal(err)
	}
	if !persisted.ExpiresAt.After(time.Now()) {
		t.Fatalf("сохранённая ExpiresAt в прошлом: %v", persisted.ExpiresAt)
	}
}

// TestTwitchDeleteSessionBumpsEpoch — DeleteSession двигает эпоху, поэтому устаревший Logout
// по старой эпохе не стирает свежую сессию.
func TestTwitchDeleteSessionBumpsEpoch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store := newMemorySecretStore()
	p := NewTwitchProvider(TwitchOptions{
		ClientID:   "client-1",
		Store:      store,
		HTTPClient: srv.Client(),
		RevokeURL:  srv.URL + "/revoke",
	})

	ctx := context.Background()
	old := TwitchSession{AccessToken: "old-access", RefreshToken: "old-refresh"}
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
	fresh := TwitchSession{AccessToken: "new-access", RefreshToken: "new-refresh"}
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

	loaded, found, err := store.Load(TwitchSessionSecretKey)
	if err != nil || !found {
		t.Fatalf("свежая сессия удалена: found=%v err=%v", found, err)
	}
	var got TwitchSession
	if err := json.Unmarshal(loaded, &got); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "new-access" {
		t.Fatalf("access = %q, want new-access", got.AccessToken)
	}
}

func TestTwitchPollCancellation(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusBadRequest, map[string]any{
			"status": 400, "message": "authorization_pending",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	p := NewTwitchProvider(TwitchOptions{
		ClientID:     "client-1",
		Store:        newMemorySecretStore(),
		HTTPClient:   srv.Client(),
		TokenURL:     srv.URL + "/token",
		PollInterval: time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := p.PollDeviceAuth(ctx, TwitchDeviceAuth{
			DeviceCode: "dev-code",
			ExpiresIn:  time.Minute,
			Interval:   time.Millisecond,
		})
		errCh <- err
	}()

	time.Sleep(10 * time.Millisecond)
	p.CancelAuth()
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("PollDeviceAuth не завершился после CancelAuth")
	}
	if p.IsRunning() {
		t.Fatal("IsRunning после отмены = true")
	}
}
