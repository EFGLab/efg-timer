package donationalerts

import (
	"sync"
	"time"

	"github.com/EFGLab/efg-timer/internal/rules"
)

// DonationApplier — донат DA → секунды таймера: amount → базовая валюта по
// курсу из правил (Rule.ToBase; валюта базы проходит как есть, курс 1) →
// секунды по правилу «X секунд за каждые N единиц базовой валюты»
// (Rule.DonationSeconds). ID доната дедуплицируется: реконнект к Centrifugo
// может доставить повтор публикации.
type DonationApplier struct {
	rule rules.Rule
	mu   sync.Mutex // вызовы из горутины чтения Run и рестартов
	seen map[int64]struct{}
}

// NewDonationApplier — applier с чистым дедуп-набором.
func NewDonationApplier(rule rules.Rule) *DonationApplier {
	return &DonationApplier{rule: rule, seen: make(map[int64]struct{})}
}

// Apply — начисляет секунды за донат и возвращает их (0 — дубликат, нулевая
// сумма или ошибка конверсии); err — только про валюту
// (rules.ErrUnknownCurrency). Донат без id (0) не дедуплицируется —
// идемпотентность невозможна без идентификатора.
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
		return 0, err // повтор такой же публикации — снова ошибка, не дубликат
	}
	if ev.ID != 0 {
		a.seen[ev.ID] = struct{}{} // запоминаем и корректные, и нулевые суммы
	}
	return a.rule.DonationSeconds(base), nil
}
