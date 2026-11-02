package donatex

import (
	"testing"
	"time"
)

// Task e03s01-t3: правило «секунды за каждые N RUB» (минимум модели
// e06s01-t1): seconds = amountInRub / perAmount * secondsPerRub.
// Тестовые донаты (isTest=true) начисляются как обычные; некорректные
// данные (perAmount <= 0, сумма <= 0) дают 0 — не паника и не минус.
func TestDonationToSeconds(t *testing.T) {
	cases := []struct {
		name          string
		d             Donation
		secondsPerRub float64
		perAmount     float64
		want          time.Duration
		tol           time.Duration // допуск для дробных значений
	}{
		{
			name:          "ровно один шаг: 100₽ при 60сек/100₽",
			d:             Donation{AmountInRub: 100},
			secondsPerRub: 60, perAmount: 100,
			want: 60 * time.Second,
		},
		{
			name:          "два с половиной шага: 250₽",
			d:             Donation{AmountInRub: 250},
			secondsPerRub: 60, perAmount: 100,
			want: 150 * time.Second,
		},
		{
			name:          "дробный результат: 33.33₽ → 19.998с",
			d:             Donation{AmountInRub: 33.33},
			secondsPerRub: 60, perAmount: 100,
			want: 20 * time.Second, tol: 5 * time.Millisecond,
		},
		{
			name:          "тестовый донат (isTest) начисляется",
			d:             Donation{AmountInRub: 100, IsTest: true},
			secondsPerRub: 60, perAmount: 100,
			want: 60 * time.Second,
		},
		{
			name:          "прочие поля payload не влияют",
			d:             Donation{ID: "x", Amount: 5, Currency: "USD", Username: "u", Message: "m", AmountInRub: 100},
			secondsPerRub: 60, perAmount: 100,
			want: 60 * time.Second,
		},
		{
			name:          "perAmount=0 — защита от деления на ноль",
			d:             Donation{AmountInRub: 100},
			secondsPerRub: 60, perAmount: 0,
			want: 0,
		},
		{
			name:          "отрицательная сумма — 0 (таймер не уменьшается)",
			d:             Donation{AmountInRub: -50},
			secondsPerRub: 60, perAmount: 100,
			want: 0,
		},
		{
			name:          "нулевой донат",
			d:             Donation{},
			secondsPerRub: 60, perAmount: 100,
			want: 0,
		},
		{
			name:          "secondsPerRub=0 — правило-заглушка",
			d:             Donation{AmountInRub: 100},
			secondsPerRub: 0, perAmount: 100,
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DonationToSeconds(tc.d, tc.secondsPerRub, tc.perAmount)
			diff := got - tc.want
			if diff < 0 {
				diff = -diff
			}
			if diff > tc.tol {
				t.Errorf("DonationToSeconds(%+v, %v, %v) = %v, want %v (±%v)",
					tc.d, tc.secondsPerRub, tc.perAmount, got, tc.want, tc.tol)
			}
		})
	}
}
