package currency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultRatesURL = "https://open.er-api.com/v6/latest"

const requestTimeout = 10 * time.Second

const maxBodyBytes = 1 << 20

const maxErrorRunes = 300

const userAgent = "EFG-Timer"

var (
	ratesURL   = DefaultRatesURL
	httpClient = &http.Client{Timeout: requestTimeout}
)

var supported = map[string]bool{
	"USD": true,
	"EUR": true,
	"RUB": true,
}

type Rates struct {
	Base      string
	Rates     map[string]float64
	UpdatedAt time.Time
}

type apiResponse struct {
	Result             string             `json:"result"`
	BaseCode           string             `json:"base_code"`
	ErrorType          string             `json:"error-type"`
	TimeLastUpdateUnix int64              `json:"time_last_update_unix"`
	Rates              map[string]float64 `json:"rates"`
}

func FetchRates(ctx context.Context, base string) (Rates, error) {
	code := strings.ToUpper(strings.TrimSpace(base))
	if code == "" {
		return Rates{}, errors.New("currency: не задана базовая валюта")
	}
	if !supported[code] {
		return Rates{}, fmt.Errorf("currency: неподдерживаемая базовая валюта: %s", code)
	}

	endpoint := ratesURL + "/" + url.PathEscape(code)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Rates{}, fmt.Errorf("currency: запрос %s: %w", endpoint, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return Rates{}, fmt.Errorf("currency: %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return Rates{}, fmt.Errorf("currency: чтение ответа %s: %w", endpoint, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		detail := truncate(string(body))
		if detail == "" {
			detail = "пустой ответ"
		}
		return Rates{}, fmt.Errorf("currency: %s: HTTP %d: %s", endpoint, resp.StatusCode, detail)
	}

	var payload apiResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return Rates{}, fmt.Errorf("currency: ответ %s: %w", endpoint, err)
	}
	if payload.Result != "success" {
		reason := truncate(payload.ErrorType)
		if reason == "" {
			reason = "без описания"
		}
		return Rates{}, fmt.Errorf("currency: %s: API %s: %s", endpoint, payload.Result, reason)
	}
	if len(payload.Rates) == 0 {
		return Rates{}, fmt.Errorf("currency: %s: API не вернул курсы", endpoint)
	}

	rates := make(map[string]float64, len(payload.Rates))
	for cur, rate := range payload.Rates {
		cur = strings.ToUpper(strings.TrimSpace(cur))
		if !supported[cur] || rate <= 0 {
			continue
		}
		rates[cur] = 1 / rate
	}
	rates[code] = 1
	if len(rates) == 1 {
		return Rates{}, fmt.Errorf("currency: %s: API не вернул курсы известных валют", endpoint)
	}

	var updated time.Time
	if payload.TimeLastUpdateUnix > 0 {
		updated = time.Unix(payload.TimeLastUpdateUnix, 0).UTC()
	}

	return Rates{Base: code, Rates: rates, UpdatedAt: updated}, nil
}

func truncate(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > maxErrorRunes {
		return string(runes[:maxErrorRunes]) + "…"
	}
	return text
}
