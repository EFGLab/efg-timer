package settings

import (
	"bytes"
	"encoding/gob"
	"time"
)

// Client IDs are public. They are variables, not constants, so a release can
// override any of them with -X:
//
//	-X github.com/EFGLab/efg-timer/internal/settings.TwitchClientID=<v>
//	-X github.com/EFGLab/efg-timer/internal/settings.DonationAlertsClientID=<v>
//	-X github.com/EFGLab/efg-timer/internal/settings.DonatexClientID=<v>
var (
	DonationAlertsClientID = "21606"
	DonatexClientID        = ""
	// Публичное Twitch-приложение владельца (Device Code Grant Flow).
	TwitchClientID = "y9tcoxzhtsy49ty5mkybksegdoymsq"
)

// DonationAlertsClientSecret — client_secret приложения DonationAlerts.
// DA не поддерживает PKCE, поэтому вход идёт по authorization code с
// секретом. Секрет — переменная сборки, в settings.json он не попадает:
//
//	-X github.com/EFGLab/efg-timer/internal/settings.DonationAlertsClientSecret=<v>
//
// Пустой секрет — вход невозможен: провайдер вернёт ошибку, и диалог
// покажет её владельцу.
var DonationAlertsClientSecret = ""

// Currency — код валюты.
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

	// BaseCurrency — валюта, в которой выражены правила начисления
	// («X секунд за каждые N <базовая валюта>»).
	BaseCurrency Currency `json:"base_currency"`

	Providers ProvidersConfig `json:"providers"`

	// Currencies — курсы относительно базовой валюты: сколько единиц
	// базовой валюты в одной единице этой. Для самой базовой валюты курс
	// равен 1.
	Currencies map[Currency]float64 `json:"currencies"`
}

type ProvidersConfig struct {
	DonateX        DonateXConfig        `json:"donatex"`
	Twitch         TwitchConfig         `json:"twitch"`
	DonationAlerts DonationAlertsConfig `json:"donationalerts"`
}

type DonateXConfig struct {
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`

	Time      TimeSettings `json:"time"`
	PerAmount int64        `json:"per_amount"`
}

type TwitchConfig struct {
	Enabled bool   `json:"enabled"`
	Login   string `json:"login,omitempty"`
	UserID  string `json:"user_id,omitempty"`

	// ExpiresAt — срок жизни access-токена. Сами токены лежат в OS credential
	// store (twitch.session). Указатель, потому что omitempty не работает
	// для time.Time.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	Tier1SubTime TimeSettings `json:"tier1_sub_time"`
	Tier2SubTime TimeSettings `json:"tier2_sub_time"`
	Tier3SubTime TimeSettings `json:"tier3_sub_time"`
	CountResub   bool         `json:"count_resub"`

	// Bits
	BitsTime      TimeSettings `json:"bits_time"`
	PerBitsAmount int64        `json:"per_bits_amount"`
}

// DonationAlertsConfig — настройки DonationAlerts. Токенов здесь нет: они
// лежат в OS credential store (donationalerts.session), в settings.json
// остаются только метаданные входа. Старый ключ "token" при чтении
// игнорируется — миграция не нужна.
type DonationAlertsConfig struct {
	Enabled bool  `json:"enabled"`
	UserID  int64 `json:"user_id,omitempty"`

	// ExpiresAt — срок жизни access-токена. Указатель, потому что omitempty
	// не работает для time.Time.
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
		// Курсы относительно базовой валюты: сколько единиц базовой валюты
		// в одной единице валюты. Для самой базовой валюты курс равен 1.
		Currencies: map[Currency]float64{
			CurrencyRUB: 1,
			CurrencyUSD: 80,
			CurrencyEUR: 88,
		},
	}
}

// legacyCurrencyRates — сломанная таблица курсов прежних сборок: значения
// стояли по чужим ключам (80 — это USD→RUB, 1.1 — это EUR→USD, 1 — USD→USD),
// поэтому донат в долларах начислялся как в рублях.
var legacyCurrencyRates = map[Currency]float64{
	CurrencyRUB: 80,
	CurrencyEUR: 1.1,
	CurrencyUSD: 1,
}

// ReanchorCurrencyRates пересчитывает курсы под новую базовую валюту:
// новая ставка = старая ставка / (старая ставка новой базы), а сама база
// получает 1. Возвращает false, если пересчитать нечем (в карте нет ставки
// новой базы) — тогда вызывающий должен обновить курсы извне.
//
// При успехе обновляется и BaseCurrency; при false ничего не меняется.
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

// MigrateCurrencyRates — точечная миграция сломанной таблицы курсов.
//
// Загрузка настроек накладывает JSON поверх дефолтов, и карта мержится по
// ключам: у пользователей с прежними сборками в settings.json лежит
// сломанная таблица, и она перебила бы исправленные дефолты. Значение,
// совпадающее со старым, заменяется новым; пользовательские курсы не
// трогаются.
//
// База не RUB: сломанная таблица прежних сборок в ней бессмысленна, поэтому
// она пересобирается из дефолтов, а сама таблица пересчитывается под базу.
func (s *Settings) MigrateCurrencyRates() {
	if s.Currencies == nil {
		return
	}
	base := s.baseCurrency()
	if base == CurrencyRUB {
		if s.BaseCurrency == "" {
			s.BaseCurrency = CurrencyRUB
		}
		for cur, legacy := range legacyCurrencyRates {
			if s.Currencies[cur] != legacy {
				continue
			}
			if fixed, ok := Default().Currencies[cur]; ok {
				s.Currencies[cur] = fixed
			}
		}
		return
	}
	if s.isLegacyCurrencyRates() {
		s.Currencies = Default().Currencies
	}
	s.ReanchorCurrencyRates(base)
}

// baseCurrency — базовая валюта с запасным значением RUB: в файлах прежних
// сборок поля не было.
func (s *Settings) baseCurrency() Currency {
	if s.BaseCurrency == "" {
		return CurrencyRUB
	}
	return s.BaseCurrency
}

// isLegacyCurrencyRates — таблица прежних сборок целиком.
func (s *Settings) isLegacyCurrencyRates() bool {
	for cur, legacy := range legacyCurrencyRates {
		if s.Currencies[cur] != legacy {
			return false
		}
	}
	return true
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
