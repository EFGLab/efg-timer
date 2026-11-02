package donationalerts_test

import (
	"errors"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/integrations/donationalerts"
	"github.com/EFGLab/efg-timer/internal/rules"
)

// newRule — «30 секунд за каждые 100 RUB» (база RUB), доллар = 90 RUB.
func newTestRule() rules.Rule {
	return rules.Rule{
		Donation:   rules.DonationRule{SecondsPerRub: 30, PerAmount: 100},
		Base:       "RUB",
		Currencies: map[string]float64{"USD": 90},
	}
}

// TestDonationSeconds — e05s02-t2: донат DA → базовая валюта по курсу из
// конфига (RUB — курс 1) → секунды по правилу; повтор доната по id не
// начисляет.
func TestDonationSeconds(t *testing.T) {
	t.Run("RUB по курсу 1", func(t *testing.T) {
		a := donationalerts.NewDonationApplier(newTestRule())
		d, err := a.Apply(donationalerts.DonationEvent{ID: 1, Amount: 100, Currency: "RUB"})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if d != 30*time.Second {
			t.Fatalf("секунды = %v, want 30s", d)
		}
	})

	t.Run("валюта по курсу из конфига", func(t *testing.T) {
		a := donationalerts.NewDonationApplier(newTestRule())
		// 10 USD × 90 = 900 RUB → 900/100 × 30 = 270 секунд
		d, err := a.Apply(donationalerts.DonationEvent{ID: 2, Amount: 10, Currency: "usd"})
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if d != 270*time.Second {
			t.Fatalf("секунды = %v, want 270s", d)
		}
	})

	t.Run("неизвестная валюта — ошибка, 0 секунд", func(t *testing.T) {
		a := donationalerts.NewDonationApplier(newTestRule())
		d, err := a.Apply(donationalerts.DonationEvent{ID: 3, Amount: 5, Currency: "GBP"})
		if !errors.Is(err, rules.ErrUnknownCurrency) {
			t.Fatalf("err = %v, want ErrUnknownCurrency", err)
		}
		if d != 0 {
			t.Fatalf("секунды = %v, want 0", d)
		}
	})

	t.Run("дубликат id не начисляет повторно", func(t *testing.T) {
		a := donationalerts.NewDonationApplier(newTestRule())
		if d, err := a.Apply(donationalerts.DonationEvent{ID: 7, Amount: 100, Currency: "RUB"}); err != nil || d != 30*time.Second {
			t.Fatalf("первый: %v, %v", d, err)
		}
		d, err := a.Apply(donationalerts.DonationEvent{ID: 7, Amount: 100, Currency: "RUB"})
		if err != nil {
			t.Fatalf("дубликат: %v", err)
		}
		if d != 0 {
			t.Fatalf("дубликат начислил %v, want 0", d)
		}
		// свежий донат с другим id после дубликата — начисляется штатно
		if d, err := a.Apply(donationalerts.DonationEvent{ID: 8, Amount: 100, Currency: "RUB"}); err != nil || d != 30*time.Second {
			t.Fatalf("свежий после дубликата: %v, %v", d, err)
		}
	})

	t.Run("нулевой и отрицательный донат — 0 секунд", func(t *testing.T) {
		a := donationalerts.NewDonationApplier(newTestRule())
		if d, err := a.Apply(donationalerts.DonationEvent{ID: 9, Amount: 0, Currency: "RUB"}); err != nil || d != 0 {
			t.Fatalf("нулевой: %v, %v", d, err)
		}
		if d, err := a.Apply(donationalerts.DonationEvent{ID: 10, Amount: -5, Currency: "RUB"}); err != nil || d != 0 {
			t.Fatalf("отрицательный: %v, %v", d, err)
		}
	})
}
