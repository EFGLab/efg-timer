package rules

import "sync"

// Store — потокобезопасное хранилище правил начисления: правило читается
// из горутин интеграций (donatex, Twitch, DonationAlerts), меняется из GUI.
// e06s01-t3: live-применение — смена действует на следующие события без
// перезапуска. Хранилище владеет собственной копией курсов (copy-on-Set и
// copy-on-Get), чтобы мутации map вызывающего не гонялись с чтением из
// горутин.
type Store struct {
	mu   sync.RWMutex
	rule Rule
}

// NewStore создаёт хранилище с начальным правилом.
func NewStore(r Rule) *Store {
	return &Store{rule: r.withCopiedCurrencies()}
}

// Get возвращает снимок текущего правила с собственной копией карты курсов.
func (s *Store) Get() Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rule.withCopiedCurrencies()
}

// Set заменяет текущее правило (вызывается из GUI).
func (s *Store) Set(r Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rule = r.withCopiedCurrencies()
}

// withCopiedCurrencies возвращает правило с собственной копией курсов.
func (r Rule) withCopiedCurrencies() Rule {
	out := make(map[string]float64, len(r.Currencies))
	for cur, rate := range r.Currencies {
		out[cur] = rate
	}
	r.Currencies = out
	return r
}
