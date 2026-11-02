package app

import (
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
)

func ruleFromSettings(s settings.Settings) rules.Rule {
	return state.RuleFromSettings(s)
}

func applyRulesToStore(st *state.AppState) {
	st.Rules.Set(ruleFromSettings(st.Settings))
}
