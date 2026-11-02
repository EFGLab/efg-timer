package twitch

import (
	"time"

	"github.com/EFGLab/efg-timer/internal/rules"
)

// SubEvent — событие channel.subscribe: новый саб.
type SubEvent struct {
	Tier string `json:"tier"`
}

// ResubEvent — событие channel.subscription.message (ре-саб).
type ResubEvent struct {
	Tier             string `json:"tier"`
	CumulativeMonths int64  `json:"cumulative_months"`
}

// SubSeconds — секунды за саб или ре-саб по тиру. Перевод тира в секунды
// живёт в internal/rules, здесь только делегирование.
func SubSeconds(rule rules.SubRule, tier string) (time.Duration, error) {
	return rule.SubSeconds(tier)
}
