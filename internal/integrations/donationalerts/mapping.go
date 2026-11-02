package donationalerts

import (
	"sync"
	"time"

	"github.com/EFGLab/efg-timer/internal/rules"
)

type DonationApplier struct {
	rule rules.Rule
	mu   sync.Mutex
	seen map[int64]struct{}
}

func NewDonationApplier(rule rules.Rule) *DonationApplier {
	return &DonationApplier{rule: rule, seen: make(map[int64]struct{})}
}

func (a *DonationApplier) Apply(ev DonationEvent) (time.Duration, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ev.ID != 0 {
		if _, dup := a.seen[ev.ID]; dup {
			return 0, nil
		}
	}
	base, err := a.rule.ToBase(ev.Amount, ev.Currency)
	if err != nil {
		return 0, err
	}
	if ev.ID != 0 {
		a.seen[ev.ID] = struct{}{}
	}
	return a.rule.DonationSeconds(base), nil
}
