package currency

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withServer подменяет адрес API на тестовый сервер и возвращает его.
func withServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	oldURL, oldClient := ratesURL, httpClient
	ratesURL = srv.URL
	t.Cleanup(func() {
		ratesURL, httpClient = oldURL, oldClient
		srv.Close()
	})
	return srv
}

// Курс API обратный: rates[cur] — сколько cur в одной единице базы. Наша
// модель — сколько единиц базы в одной единице cur.
func TestFetchRatesInvertsRates(t *testing.T) {
	var gotPath, gotAccept, gotUA string
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAccept = r.Header.Get("Accept")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"result":"success","base_code":"USD","time_last_update_unix":1700000000,`+
			`"rates":{"USD":1,"RUB":83.2,"EUR":0.89,"JPY":148.5}}`)
	})

	rates, err := FetchRates(context.Background(), "usd")
	if err != nil {
		t.Fatalf("FetchRates: %v", err)
	}

	if gotPath != "/USD" {
		t.Errorf("путь запроса = %q, want /USD", gotPath)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if gotUA == "" {
		t.Error("User-Agent пуст")
	}
	if rates.Base != "USD" {
		t.Errorf("Base = %q, want USD", rates.Base)
	}
	if want := 1 / 83.2; math.Abs(rates.Rates["RUB"]-want) > 1e-12 {
		t.Errorf("курс RUB = %v, want %v", rates.Rates["RUB"], want)
	}
	if want := 1 / 0.89; math.Abs(rates.Rates["EUR"]-want) > 1e-12 {
		t.Errorf("курс EUR = %v, want %v", rates.Rates["EUR"], want)
	}
	if rates.Rates["USD"] != 1 {
		t.Errorf("курс базы USD = %v, want 1", rates.Rates["USD"])
	}
	if _, ok := rates.Rates["JPY"]; ok {
		t.Error("неизвестная валюта JPY попала в карту")
	}
	if want := time.Unix(1700000000, 0).UTC(); !rates.UpdatedAt.Equal(want) {
		t.Errorf("UpdatedAt = %v, want %v", rates.UpdatedAt, want)
	}
}

// Ошибка HTTP: в текст попадает тело ответа, обрезанное до 300 рун.
func TestFetchRatesHTTPError(t *testing.T) {
	body := strings.Repeat("a", 1000)
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, body)
	})

	_, err := FetchRates(context.Background(), "USD")
	if err == nil {
		t.Fatal("ожидалась ошибка HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("в ошибке нет кода ответа: %v", err)
	}
	if got := strings.Count(err.Error(), "a"); got != maxErrorRunes {
		t.Errorf("длина тела в ошибке = %d, want %d", got, maxErrorRunes)
	}
}

// Битый JSON — ошибка.
func TestFetchRatesInvalidJSON(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":"success","rates":`)
	})

	if _, err := FetchRates(context.Background(), "USD"); err == nil {
		t.Fatal("ожидалась ошибка разбора ответа")
	}
}

// result != success — ошибка.
func TestFetchRatesAPIError(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":"error","error-type":"unsupported-code"}`)
	})

	_, err := FetchRates(context.Background(), "USD")
	if err == nil {
		t.Fatal("ожидалась ошибка API")
	}
	if !strings.Contains(err.Error(), "unsupported-code") {
		t.Errorf("в ошибке нет типа ошибки API: %v", err)
	}
}

// Пустой список курсов — ошибка.
func TestFetchRatesEmptyRates(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"result":"success","base_code":"USD","rates":{}}`)
	})

	if _, err := FetchRates(context.Background(), "USD"); err == nil {
		t.Fatal("ожидалась ошибка пустых курсов")
	}
}

// Отмена контекста прерывает запрос.
func TestFetchRatesContextCancelled(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	if _, err := FetchRates(ctx, "USD"); err == nil {
		t.Fatal("ожидалась ошибка отмены контекста")
	}
}

// Таймаут HTTP-клиента обрывает запрос.
func TestFetchRatesTimeout(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	})
	httpClient = &http.Client{Timeout: 50 * time.Millisecond}

	_, err := FetchRates(context.Background(), "USD")
	if err == nil {
		t.Fatal("ожидалась ошибка таймаута")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("ошибка не про таймаут: %v", err)
	}
}

// Неподдерживаемая база и пустая база — ошибки до запроса.
func TestFetchRatesBadBase(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("сервер не должен вызываться")
	})

	if _, err := FetchRates(context.Background(), "GBP"); err == nil {
		t.Error("ожидалась ошибка неподдерживаемой базы")
	}
	if _, err := FetchRates(context.Background(), "  "); err == nil {
		t.Error("ожидалась ошибка пустой базы")
	}
}
