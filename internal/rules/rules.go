// Package rules — модель правил начисления: канонический источник для всех
// интеграций. Правило донатов: seconds = amountInBase / per_amount *
// seconds_per_rub.
package rules

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// DonationRule — правило «X секунд за каждые N единиц базовой валюты».
type DonationRule struct {
	SecondsPerRub float64 `json:"seconds_per_rub"` // X: секунд за шаг
	PerAmount     float64 `json:"per_amount"`      // N: размер шага в базовой валюте (N > 0)
}

// SubRule — фиксированные секунды за сабы по тирам Twitch. Ре-саб считается
// по правилу своего тира, отдельного правила для него нет. CountResub —
// считать ли ре-сабы вовсе.
type SubRule struct {
	Tier1000   int64 `json:"tier_1000"`
	Tier2000   int64 `json:"tier_2000"`
	Tier3000   int64 `json:"tier_3000"`
	CountResub bool  `json:"count_resub"`
}

// Rule — полная модель правил. Базовая валюта всегда задана: сумма доната
// приводится к ней, а правило доната выражено в её единицах.
type Rule struct {
	Donation   DonationRule       `json:"donation"`
	Sub        SubRule            `json:"sub"`
	Base       string             `json:"base"`       // код базовой валюты, напр. "RUB"
	Currencies map[string]float64 `json:"currencies"` // код → единиц базовой валюты за единицу этой
}

// ErrUnknownCurrency — валюта не найдена в Currencies или курс неположительный.
var ErrUnknownCurrency = errors.New("rules: неизвестная валюта")

// ErrUnknownTier — тир подписки не распознан (prime и т.п.).
var ErrUnknownTier = errors.New("rules: неизвестный тир")

// DonationSeconds — секунды за донат в базовой валюте. PerAmount <= 0 или
// amountInBase <= 0 → 0: некорректные данные не двигают таймер.
func DonationSeconds(amountInBase float64, r DonationRule) time.Duration {
	if r.PerAmount <= 0 || amountInBase <= 0 {
		return 0
	}
	return time.Duration(amountInBase / r.PerAmount * r.SecondsPerRub * float64(time.Second))
}

// DonationSeconds — секунды за донат по правилу этой модели.
func (r Rule) DonationSeconds(amountInBase float64) time.Duration {
	return DonationSeconds(amountInBase, r.Donation)
}

// SubSeconds — секунды за саб или ре-саб по тиру. Неизвестный тир — ошибка.
func (r SubRule) SubSeconds(tier string) (time.Duration, error) {
	switch tier {
	case "1000":
		return time.Duration(r.Tier1000) * time.Second, nil
	case "2000":
		return time.Duration(r.Tier2000) * time.Second, nil
	case "3000":
		return time.Duration(r.Tier3000) * time.Second, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrUnknownTier, tier)
	}
}

// SubSeconds — секунды за саб или ре-саб по правилу этой модели.
func (r Rule) SubSeconds(tier string) (time.Duration, error) {
	return r.Sub.SubSeconds(tier)
}

// ToBase — сумма в базовой валюте: валюта базы проходит 1:1 (курс из карты не
// берётся), остальное — через Currencies (регистр кода не важен). Неизвестная
// валюта, неположительный курс или незаданная база — ErrUnknownCurrency:
// вызывающий решает (обычно не начислять + лог).
func (r Rule) ToBase(amount float64, currency string) (float64, error) {
	base := strings.ToUpper(r.Base)
	if base == "" {
		return 0, fmt.Errorf("%w: не задана базовая валюта", ErrUnknownCurrency)
	}
	cur := strings.ToUpper(currency)
	if cur == base {
		return amount, nil
	}
	rate, ok := r.Currencies[cur]
	if !ok || rate <= 0 {
		return 0, fmt.Errorf("%w: %s", ErrUnknownCurrency, currency)
	}
	return amount * rate, nil
}
