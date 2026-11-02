package donationalerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/integrations/donationalerts"
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/timer"
)

// TestE2E — e05s02-t3: сквозной пайплайн против мока протокола DA:
// соединение → subscribe → donation-событие → таймер вырос на ожидаемые
// секунды (100.5 RUB при правиле 30с/100RUB → +30.15с).
func TestE2E(t *testing.T) {
	m := newCentrifugoMock(t, true) // публикация: id=5, amount=100.5, RUB

	rule := rules.Rule{
		Donation:   rules.DonationRule{SecondsPerRub: 30, PerAmount: 100},
		Base:       "RUB",
		Currencies: map[string]float64{"USD": 90},
	}
	tm := timer.New(10 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := donationalerts.RunDonations(ctx,
		donationalerts.CentrifugoOptions{
			WSURL:       m.wsURL(),
			SocketToken: "sock-token",
			Channel:     "$alerts:donation_1",
			Client:      m.daClient(),
		},
		donationalerts.NewDonationApplier(rule),
		func(d time.Duration) { tm.Add(timer.SourceDonationalerts, d, "донат") },
		nil,
	)
	if err != nil {
		t.Fatalf("RunDonations: %v", err)
	}

	rem := tm.Remaining()
	// база 10с + 30.15с; тики и задержки мока могут съесть до 3с
	if rem < 37*time.Second || rem >= 41*time.Second {
		t.Fatalf("Remaining = %v, want в диапазоне [37s, 41s) — таймер не вырос на донат", rem)
	}
}
