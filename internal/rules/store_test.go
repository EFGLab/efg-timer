package rules

import (
	"sync"
	"testing"
	"time"
)

// Task e06s01-t3: live-применение — правила живут в потокобезопасном Store;
// смена из GUI действует на следующие события интеграций без перезапуска.

func TestApplyLive(t *testing.T) {
	// Один и тот же экземпляр хранилища: сцена «до» и «после» Set.
	st := NewStore(Rule{
		Donation:   DonationRule{SecondsPerRub: 60, PerAmount: 100},
		Sub:        SubRule{Tier1000: 300, Tier2000: 600, Tier3000: 900},
		Base:       "RUB",
		Currencies: map[string]float64{"USD": 80, "EUR": 90},
	})

	if got := st.Get().DonationSeconds(100); got != 60*time.Second {
		t.Errorf("до смены: DonationSeconds(100) = %v, want 60s", got)
	}
	if got, err := st.Get().SubSeconds("1000"); err != nil || got != 300*time.Second {
		t.Errorf("до смены: SubSeconds(1000) = %v, %v; want 300s, nil", got, err)
	}

	st.Set(Rule{
		Donation:   DonationRule{SecondsPerRub: 30, PerAmount: 100},
		Sub:        SubRule{Tier1000: 100, Tier2000: 200, Tier3000: 300},
		Base:       "RUB",
		Currencies: map[string]float64{"USD": 100, "EUR": 90},
	})

	if got := st.Get().DonationSeconds(100); got != 30*time.Second {
		t.Errorf("после смены: DonationSeconds(100) = %v, want 30s (live)", got)
	}
	if got, err := st.Get().SubSeconds("1000"); err != nil || got != 100*time.Second {
		t.Errorf("после смены: SubSeconds(1000) = %v, %v; want 100s, nil (live)", got, err)
	}
	if got, err := st.Get().SubSeconds("2000"); err != nil || got != 200*time.Second {
		t.Errorf("после смены: SubSeconds(2000) = %v, %v; want 200s, nil (live)", got, err)
	}

	// Курс тоже живой: 5 USD теперь 500 ₽ → 500/100*30 = 150с.
	base, err := st.Get().ToBase(5, "USD")
	if err != nil || base != 500 {
		t.Fatalf("после смены: ToBase(5, USD) = %v, %v; want 500, nil", base, err)
	}
	if got := st.Get().DonationSeconds(base); got != 150*time.Second {
		t.Errorf("после смены: DonationSeconds(500) = %v, want 150s", got)
	}

	// Хранилище владеет своей копией курсов: мутация map вызывающего
	// после Set не меняет правило внутри (иначе — гонка map с горутинами
	// интеграций).
	ext := map[string]float64{"USD": 80}
	st.Set(Rule{Base: "RUB", Currencies: ext})
	ext["USD"] = 999
	if base, _ := st.Get().ToBase(1, "USD"); base != 80 {
		t.Errorf("хранилище шарит map вызывающего: ToBase(1, USD) = %v, want 80", base)
	}

	// Get — снимок: мутация карты из снимка не меняет хранилище.
	snapshot := st.Get()
	snapshot.Currencies["USD"] = 999
	if base, _ := st.Get().ToBase(1, "USD"); base != 80 {
		t.Errorf("Get отдаёт общую карту: ToBase(1, USD) = %v, want 80", base)
	}
}

func TestStoreConcurrent(t *testing.T) {
	// Гонки Set/Get не ломают хранилище; сам мьютекс проверяет детектор
	// -race в общей батарее.
	st := NewStore(Rule{
		Donation:   DonationRule{SecondsPerRub: 60, PerAmount: 100},
		Base:       "RUB",
		Currencies: map[string]float64{"USD": 80},
	})
	var wg sync.WaitGroup
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				st.Set(Rule{
					Donation:   DonationRule{SecondsPerRub: float64(30 + 30*(i%2)), PerAmount: 100},
					Base:       "RUB",
					Currencies: map[string]float64{"USD": 80, "EUR": 90},
				})
			}
		}()
	}
	for r := 0; r < 2; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				rule := st.Get()
				_ = rule.DonationSeconds(100)
				_, _ = rule.ToBase(5, "USD")
			}
		}()
	}
	wg.Wait()
	// Финальное состояние валидно: правило из последнего Set применимо.
	if got := st.Get().DonationSeconds(100); got != 30*time.Second && got != 60*time.Second {
		t.Errorf("после гонки Set/Get: DonationSeconds(100) = %v, want 30s или 60s", got)
	}
}
