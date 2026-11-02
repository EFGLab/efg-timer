package donatex

import (
	"context"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/timer"
)

// e03s01-t4: E2E — mock-хаб шлёт DonationCreated, прод-цепочка начисления
// (payload → секунды по правилу → таймер) увеличивает таймер на ожидаемое
// число секунд. Обвязка в тесте повторяет будущую обвязку main: handler
// внутри неё вызывает DonationToSeconds и Timer.Add.
func TestE2EDonationGrowsTimer(t *testing.T) {
	hub := &fakeHub{events: []fakeEvent{{
		// payload в проводном формате (camelCase), как реальный DonationCreated;
		// receiver разберёт его в Donation.
		target: "DonationCreated",
		payload: map[string]any{
			"id": "d1", "amountInRub": 250.0, "username": "viewer", "isTest": true,
		},
	}}}
	url, _ := startMockHub(t, hub)

	tm := timer.New(5 * time.Minute)
	base := tm.Remaining()

	c := newClient(url, "tok", func(d Donation) {
		tm.Add(timer.SourceDonatex, DonationToSeconds(d, 60, 100), "донат DonateX") // 250₽ → 150с
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go c.Run(ctx)

	want := base + 150*time.Second
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if got := tm.Remaining(); got == want {
			// Начисление растёт остаток; исходная длительность (duration)
			// в текущем движке не меняется.
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("таймер не вырос за 4с: Remaining() = %v, want %v", tm.Remaining(), want)
}
