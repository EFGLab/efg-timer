package donatex

import (
	"time"

	"github.com/EFGLab/efg-timer/internal/rules"
)

// DonationToSeconds — правило «X секунд за каждые N RUB». С e06s01-t1 формула
// живёт в internal/rules (канонический источник для всех интеграций), здесь
// обёртка для совместимости (verify e03s01-t3 и E2E). Тестовые донаты
// (isTest=true) начисляются как обычные; защита от кривого правила и
// неположительных сумм — внутри rules.DonationSeconds (0 секунд, не паника).
func DonationToSeconds(d Donation, secondsPerRub, perAmount float64) time.Duration {
	return rules.DonationSeconds(d.AmountInRub, rules.DonationRule{
		SecondsPerRub: secondsPerRub,
		PerAmount:     perAmount,
	})
}
