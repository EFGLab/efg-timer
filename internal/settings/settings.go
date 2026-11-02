package settings

import (
	"bytes"
	"encoding/gob"
	"time"
)

// -X github.com/EFGLab/efg-timer/internal/settings.TwitchClientID=<v>
// -X github.com/EFGLab/efg-timer/internal/settings.DonationAlertsClientID=<v>
// -X github.com/EFGLab/efg-timer/internal/settings.DonatexClientID=<v>
var (
	DonationAlertsClientID = ""
	DonatexClientID        = ""
	TwitchClientID         = ""
)

type Currency string

const (
	CurrencyUSD Currency = "USD"
	CurrencyEUR Currency = "EUR"
	CurrencyRUB Currency = "RUB"
)

type Settings struct {
	Version         int8         `json:"version"`
	DefaultDuration TimeSettings `json:"default_duration"`
	Port            int          `json:"port"`

	Providers    ProvidersConfig      `json:"providers"`
	BaseCurrency Currency             `json:"base_currency"`
	Currencies   map[Currency]float64 `json:"currencies"`
}

type ProvidersConfig struct {
	DonateX        DonateXConfig        `json:"donatex"`
	Twitch         TwitchConfig         `json:"twitch"`
	DonationAlerts DonationAlertsConfig `json:"donationalerts"`
}

type DonateXConfig struct {
	Enabled bool `json:"enabled"`

	Time      TimeSettings `json:"time"`
	PerAmount int64        `json:"per_amount"`
}

type TwitchConfig struct {
	Enabled bool   `json:"enabled"`
	Login   string `json:"login,omitempty"`
	UserID  string `json:"user_id,omitempty"`

	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	Tier1SubTime TimeSettings `json:"tier1_sub_time"`
	Tier2SubTime TimeSettings `json:"tier2_sub_time"`
	Tier3SubTime TimeSettings `json:"tier3_sub_time"`
	CountResub   bool         `json:"count_resub"`

	BitsTime      TimeSettings `json:"bits_time"`
	PerBitsAmount int64        `json:"per_bits_amount"`
}

type DonationAlertsConfig struct {
	Enabled bool  `json:"enabled"`
	UserID  int64 `json:"user_id,omitempty"`

	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	Time      TimeSettings `json:"time"`
	PerAmount int64        `json:"per_amount"`
}

func Default() Settings {
	return Settings{
		Version: 1,
		DefaultDuration: TimeSettings{
			Value: 5,
			Unit:  TimeUnitMinute,
		},
		Port:         8089,
		BaseCurrency: CurrencyRUB,
		Providers: ProvidersConfig{
			DonateX: DonateXConfig{
				Time: TimeSettings{
					Value: 1,
					Unit:  TimeUnitMinute,
				},
				PerAmount: 100,
			},
			DonationAlerts: DonationAlertsConfig{
				Time: TimeSettings{
					Value: 1,
					Unit:  TimeUnitMinute,
				},
				PerAmount: 100,
			},
			Twitch: TwitchConfig{
				BitsTime: TimeSettings{
					Value: 1,
					Unit:  TimeUnitMinute,
				},
				PerBitsAmount: 100,

				Tier1SubTime: TimeSettings{
					Value: 1,
					Unit:  TimeUnitMinute,
				},
				Tier2SubTime: TimeSettings{
					Value: 5,
					Unit:  TimeUnitMinute,
				},
				Tier3SubTime: TimeSettings{
					Value: 10,
					Unit:  TimeUnitMinute,
				},
				CountResub: true,
			},
		},
		Currencies: map[Currency]float64{
			CurrencyRUB: 1,
			CurrencyUSD: 80,
			CurrencyEUR: 88,
		},
	}
}

func (s *Settings) ReanchorCurrencyRates(newBase Currency) bool {
	if s.Currencies == nil {
		return false
	}
	baseRate, ok := s.Currencies[newBase]
	if !ok || baseRate <= 0 {
		return false
	}
	for cur, rate := range s.Currencies {
		if cur == newBase {
			s.Currencies[cur] = 1
			continue
		}
		s.Currencies[cur] = rate / baseRate
	}
	s.BaseCurrency = newBase
	return true
}

func (s *Settings) baseCurrency() Currency {
	if s.BaseCurrency == "" {
		return CurrencyRUB
	}
	return s.BaseCurrency
}

func (s Settings) Copy() (Settings, error) {
	var dst Settings
	var buf bytes.Buffer

	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		return dst, err
	}

	if err := gob.NewDecoder(&buf).Decode(&dst); err != nil {
		return dst, err
	}

	return dst, nil
}
