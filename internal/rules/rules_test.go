package rules

import (
	"errors"
	"testing"
	"time"
)

// e06s01-t1: модель правил начисления — канонический источник для всех
// интеграций: e03 Donatex уже использует формулу донатов; e04 Twitch возьмёт
// SubSeconds; e05 DonationAlerts — ToBase + DonationSeconds.

func TestDonationSeconds(t *testing.T) {
	r := Rule{Base: "RUB", Donation: DonationRule{SecondsPerRub: 60, PerAmount: 100}}

	cases := []struct {
		name         string
		amountInBase float64
		want         time.Duration
	}{
		{"ровно один шаг: 100", 100, 60 * time.Second},
		{"два с половиной шага: 250", 250, 150 * time.Second},
		{"нулевой донат", 0, 0},
		{"отрицательный донат не уменьшает таймер", -50, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.DonationSeconds(tc.amountInBase); got != tc.want {
				t.Errorf("DonationSeconds(%v) = %v, want %v", tc.amountInBase, got, tc.want)
			}
		})
	}

	// PerAmount — шаг в базовой валюте: та же сумма в другой базе даёт
	// те же секунды (правило выражено в единицах базы).
	usd := Rule{Base: "USD", Donation: DonationRule{SecondsPerRub: 60, PerAmount: 100}}
	if got := usd.DonationSeconds(100); got != 60*time.Second {
		t.Errorf("база USD: DonationSeconds(100) = %v, want 60s", got)
	}

	// защита от деления на ноль: кривое правило — 0 секунд, не паника
	broken := Rule{Base: "RUB", Donation: DonationRule{SecondsPerRub: 60, PerAmount: 0}}
	if got := broken.DonationSeconds(100); got != 0 {
		t.Errorf("PerAmount=0: DonationSeconds = %v, want 0", got)
	}
}

func TestSubSeconds(t *testing.T) {
	r := Rule{Sub: SubRule{Tier1000: 300, Tier2000: 600, Tier3000: 900}}

	cases := []struct {
		name string
		tier string
		want time.Duration
	}{
		{"tier 1000", "1000", 300 * time.Second},
		{"tier 2000", "2000", 600 * time.Second},
		{"tier 3000", "3000", 900 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.SubSeconds(tc.tier)
			if err != nil {
				t.Fatalf("SubSeconds(%q): %v", tc.tier, err)
			}
			if got != tc.want {
				t.Errorf("SubSeconds(%q) = %v, want %v", tc.tier, got, tc.want)
			}
		})
	}
}

// TestSubSecondsUnknownTier — неизвестный тир (prime и т.п.) — ошибка:
// вызывающий логирует и не начисляет.
func TestSubSecondsUnknownTier(t *testing.T) {
	r := Rule{Sub: SubRule{Tier1000: 300, Tier2000: 600, Tier3000: 900}}

	for _, tier := range []string{"prime", "0", "9999", ""} {
		got, err := r.SubSeconds(tier)
		if !errors.Is(err, ErrUnknownTier) {
			t.Errorf("SubSeconds(%q): err = %v, want ErrUnknownTier", tier, err)
		}
		if got != 0 {
			t.Errorf("SubSeconds(%q) = %v, want 0", tier, got)
		}
	}
}

// TestSubSecondsResubByTier — ре-саб считается по правилу своего тира:
// отдельного правила ре-саба в модели нет.
func TestSubSecondsResubByTier(t *testing.T) {
	r := Rule{Sub: SubRule{Tier1000: 300, Tier2000: 600, Tier3000: 900}}

	for _, tier := range []string{"1000", "2000", "3000"} {
		sub, err := r.SubSeconds(tier)
		if err != nil {
			t.Fatalf("SubSeconds(%q): %v", tier, err)
		}
		resub, err := r.SubSeconds(tier)
		if err != nil {
			t.Fatalf("SubSeconds(%q) (ре-саб): %v", tier, err)
		}
		if sub != resub {
			t.Errorf("ре-саб тира %q: %v, want %v (как у саба)", tier, resub, sub)
		}
	}
}

func TestToBase(t *testing.T) {
	r := Rule{Base: "RUB", Currencies: map[string]float64{"USD": 80, "EUR": 90}}

	cases := []struct {
		name     string
		amount   float64
		currency string
		want     float64
	}{
		{"валюта базы — 1:1", 250, "RUB", 250},
		{"USD по курсу", 5, "USD", 400},
		{"EUR по курсу", 2, "EUR", 180},
		{"регистр кода не важен", 5, "usd", 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.ToBase(tc.amount, tc.currency)
			if err != nil {
				t.Fatalf("ToBase(%v, %s): %v", tc.amount, tc.currency, err)
			}
			if got != tc.want {
				t.Errorf("ToBase(%v, %s) = %v, want %v", tc.amount, tc.currency, got, tc.want)
			}
		})
	}

	// база не RUB: базовая валюта — 1:1, остальное — по карте курсов
	usd := Rule{Base: "USD", Currencies: map[string]float64{"RUB": 80}}
	if got, err := usd.ToBase(5, "USD"); err != nil || got != 5 {
		t.Errorf("база USD: ToBase(5, USD) = %v, %v; want 5, nil", got, err)
	}
	if got, err := usd.ToBase(5, "RUB"); err != nil || got != 400 {
		t.Errorf("база USD: ToBase(5, RUB) = %v, %v; want 400, nil", got, err)
	}

	// регистр кода базы не важен
	lower := Rule{Base: "rub", Currencies: map[string]float64{"USD": 80}}
	if got, err := lower.ToBase(5, "RUB"); err != nil || got != 5 {
		t.Errorf("база rub: ToBase(5, RUB) = %v, %v; want 5, nil", got, err)
	}

	// неизвестная валюта — sentinel-ошибка: вызывающий решает (обычно
	// не начислять и залогировать)
	if _, err := r.ToBase(5, "GBP"); !errors.Is(err, ErrUnknownCurrency) {
		t.Errorf("GBP: err = %v, want ErrUnknownCurrency", err)
	}

	// нулевой курс в конфиге — не кредитуем (как неизвестная валюта)
	zero := Rule{Base: "RUB", Currencies: map[string]float64{"USD": 0}}
	if got, err := zero.ToBase(5, "USD"); err == nil || got != 0 {
		t.Errorf("курс 0: ToBase = %v, %v; want 0 + ошибка", got, err)
	}

	// незаданная база — ошибка, а не молчаливый RUB
	noBase := Rule{Currencies: map[string]float64{"USD": 80}}
	if got, err := noBase.ToBase(5, "RUB"); !errors.Is(err, ErrUnknownCurrency) || got != 0 {
		t.Errorf("без базы: ToBase = %v, %v; want 0 + ErrUnknownCurrency", got, err)
	}
}
