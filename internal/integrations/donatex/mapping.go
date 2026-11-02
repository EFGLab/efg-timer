package donatex

import (
	"time"

	"github.com/EFGLab/efg-timer/internal/rules"
)

func DonationToSeconds(d Donation, secondsPerRub, perAmount float64) time.Duration {
	return rules.DonationSeconds(d.AmountInRub, rules.DonationRule{
		SecondsPerRub: secondsPerRub,
		PerAmount:     perAmount,
	})
}
