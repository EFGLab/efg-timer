package state

import (
	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/timer"
)

type AppState struct {
	Timer     *timer.Timer
	Settings  settings.Settings
	Providers providers.Providers

	// Rules — живое правило начисления: потоки событий читают его в
	// момент события, GUI обновляет при изменении настроек.
	Rules *rules.Store

	RootState     *RootState
	SettingsState *SettingsState
}

func InitialAppState() *AppState {
	s := settings.Default()

	return &AppState{
		Settings:      s,
		SettingsState: initialSettingsState(),
		RootState:     initialRootState(),
		Rules:         rules.NewStore(RuleFromSettings(s)),
	}
}

// RuleFromSettings — правило начисления из настроек: донат («X секунд за
// каждые N единиц базовой валюты»), тиры сабов, базовая валюта и курсы
// относительно неё.
//
// Сборка живёт здесь, а не в internal/app: internal/app импортирует
// internal/state, обратная связь дала бы цикл импортов.
func RuleFromSettings(s settings.Settings) rules.Rule {
	da := s.Providers.DonationAlerts
	twitch := s.Providers.Twitch

	currencies := make(map[string]float64, len(s.Currencies))
	for code, rate := range s.Currencies {
		currencies[string(code)] = rate
	}

	return rules.Rule{
		Donation: rules.DonationRule{
			SecondsPerRub: da.Time.ToDuration().Seconds(),
			PerAmount:     float64(da.PerAmount),
		},
		Sub: rules.SubRule{
			Tier1000:   int64(twitch.Tier1SubTime.ToDuration().Seconds()),
			Tier2000:   int64(twitch.Tier2SubTime.ToDuration().Seconds()),
			Tier3000:   int64(twitch.Tier3SubTime.ToDuration().Seconds()),
			CountResub: twitch.CountResub,
		},
		Base:       string(s.BaseCurrency),
		Currencies: currencies,
	}
}
