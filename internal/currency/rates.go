// Package currency — получение курсов валют из внешнего API.
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

// DefaultRatesURL — бесплатный API курсов валют (exchangerate-api.com, tier без
// ключа и регистрации): обновляется раз в сутки и публикует RUB.
const DefaultRatesURL = "https://open.er-api.com/v6/latest"

// requestTimeout — таймаут HTTP-запроса курсов.
const requestTimeout = 10 * time.Second

// maxBodyBytes — предел тела ответа.
const maxBodyBytes = 1 << 20

// maxErrorRunes — длина текста ошибки, попадающей в UI.
const maxErrorRunes = 300

// userAgent — User-Agent с названием приложения.
const userAgent = "EFG-Timer"

var (
	// ratesURL — адрес API; тесты подменяют его на httptest-сервер.
	ratesURL = DefaultRatesURL
	// httpClient — клиент с таймаутом; тесты уменьшают таймаут.
	httpClient = &http.Client{Timeout: requestTimeout}
)

// supported — валюты, которые умеет считать приложение. Список совпадает с
// настройками; пакет не зависит от internal/settings, чтобы не было цикла.
var supported = map[string]bool{
	"USD": true,
	"EUR": true,
	"RUB": true,
}

// Rates — курсы относительно базовой валюты.
type Rates struct {
	Base      string             // код базовой валюты
	Rates     map[string]float64 // код валюты → единиц базовой валюты за единицу этой
	UpdatedAt time.Time          // время обновления данных (по данным API)
}

// apiResponse — ответ API: курс валюты = сколько её единиц в одной единице
// базы (обратное к модели настроек).
type apiResponse struct {
	Result             string             `json:"result"`
	BaseCode           string             `json:"base_code"`
	ErrorType          string             `json:"error-type"`
	TimeLastUpdateUnix int64              `json:"time_last_update_unix"`
	Rates              map[string]float64 `json:"rates"`
}

// FetchRates запрашивает курсы относительно базовой валюты.
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

	// API отдаёт обратное: rates[cur] — сколько cur в одной единице базы.
	// Наша модель — сколько единиц базы в одной единице cur, поэтому курс
	// разворачивается.
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

// truncate — обрезает текст ошибки, чтобы HTML от прокси не залил UI.
func truncate(text string) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > maxErrorRunes {
		return string(runes[:maxErrorRunes]) + "…"
	}
	return text
}
