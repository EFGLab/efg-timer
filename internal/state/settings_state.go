package state

import (
	"context"
	"time"

	"github.com/EFGLab/efg-timer/internal/settings"
)

type SettingSection string

const (
	SettingSectionTwitch         SettingSection = "twitch"
	SettingSectionDonationAlerts SettingSection = "donation_alerts"
	SettingSectionDonateX        SettingSection = "donatex"
	SettingSectionCurrencies     SettingSection = "currencies"
)

// TwitchAuthState — UI-состояние незавершённого Device Code Flow. Живёт
// только в памяти; токены сюда не попадают.
type TwitchAuthState struct {
	Pending         bool
	UserCode        string
	VerificationURI string
	Error           string
	Cancel          context.CancelFunc
}

// DonationAlertsAuthState — UI-состояние незавершённого входа
// DonationAlerts. Живёт только в памяти: ни токенов, ни client_secret
// здесь не хранится. AuthorizeURL — ссылка для браузера, её показывают
// в диалоге, пока вход идёт.
type DonationAlertsAuthState struct {
	Pending      bool
	AuthorizeURL string
	Error        string
	Cancel       context.CancelFunc
}

// CurrenciesState — UI-состояние раздела «Валюты». Живёт только в памяти:
// сами курсы лежат в черновике настроек.
type CurrenciesState struct {
	// Pending — идёт обновление курсов из внешнего API.
	Pending bool
	// Error — текст последней ошибки обновления курсов.
	Error string
	// UpdatedAt — время последнего успешного обновления курсов.
	UpdatedAt time.Time
	// NeedsRefresh — база сменилась, а пересчитать курсы было нечем:
	// значения надо взять из API.
	NeedsRefresh bool
}

type SettingsState struct {
	CurrentSection SettingSection
	Settings       settings.Settings
	TwitchAuth     TwitchAuthState
	// DonationAlertsAuth — состояние входа DA: «вход идёт», ссылка для
	// браузера и текст последней ошибки.
	DonationAlertsAuth DonationAlertsAuthState
	// Currencies — состояние раздела «Валюты»: обновление курсов из API.
	Currencies CurrenciesState
}

func initialSettingsState() *SettingsState {
	return &SettingsState{
		CurrentSection: SettingSectionTwitch,
	}
}
