// Живые правила начисления: сборка из настроек и обновление в сторе.
//
// Правило хранится в rules.Store и обновляется при сохранении настроек,
// поэтому потоки событий читают актуальные правила без перезапуска.
package app

import (
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
)

// ruleFromSettings собирает правило начисления из настроек: правило доната
// («X секунд за каждые N <базовая валюта>»), тиры сабов, базовая валюта и
// курсы относительно неё.
//
// Сама сборка — в state.RuleFromSettings: там же InitialAppState собирает
// правило по умолчанию, а internal/app импортирует internal/state (обратная
// связь дала бы цикл импортов).
func ruleFromSettings(s settings.Settings) rules.Rule {
	return state.RuleFromSettings(s)
}

// applyRulesToStore обновляет живое правило в сторе по текущим настройкам.
func applyRulesToStore(st *state.AppState) {
	st.Rules.Set(ruleFromSettings(st.Settings))
}
