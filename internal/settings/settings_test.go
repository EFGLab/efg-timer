package settings

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestCopyPreservesTwitchAuthMetadata(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour).Round(time.Second)
	want := Default()
	want.Providers.Twitch.Enabled = true
	want.Providers.Twitch.Login = "streamer"
	want.Providers.Twitch.UserID = "42"
	want.Providers.Twitch.ExpiresAt = &expiresAt

	got, err := want.Copy()
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if got.Providers.Twitch.ExpiresAt == nil || !got.Providers.Twitch.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", got.Providers.Twitch.ExpiresAt, expiresAt)
	}
	if got.Providers.Twitch.ExpiresAt == want.Providers.Twitch.ExpiresAt {
		t.Fatal("ExpiresAt: Copy должен делать глубокую копию, указатели совпали")
	}
	// Сравниваем остальные поля: указатели различаются, уравниваем их.
	got.Providers.Twitch.ExpiresAt = want.Providers.Twitch.ExpiresAt
	if got.Providers.Twitch != want.Providers.Twitch {
		t.Fatalf("Twitch = %+v, want %+v", got.Providers.Twitch, want.Providers.Twitch)
	}
}

// omitempty для time.Time не работает, поэтому ExpiresAt — указатель: без
// входа поле не должно попадать в settings.json.
func TestTwitchConfigOmitsEmptyExpiresAt(t *testing.T) {
	data, err := json.Marshal(TwitchConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "expires_at") {
		t.Fatalf("пустой ExpiresAt попал в JSON: %s", data)
	}
}

// Курс валюты — сколько единиц базовой валюты в одной единице этой. База по
// умолчанию RUB, остальные валюты пересчитываются в базу умножением на курс.
func TestDefaultCurrencyRates(t *testing.T) {
	def := Default()
	if def.BaseCurrency != CurrencyRUB {
		t.Fatalf("BaseCurrency = %q, want %q", def.BaseCurrency, CurrencyRUB)
	}

	got := def.Currencies
	want := map[Currency]float64{
		CurrencyRUB: 1,
		CurrencyUSD: 80,
		CurrencyEUR: 88,
	}
	if len(got) != len(want) {
		t.Fatalf("Currencies = %v, want %v", got, want)
	}
	for cur, rate := range want {
		if got[cur] != rate {
			t.Errorf("курс %s = %v, want %v", cur, got[cur], rate)
		}
	}
}

// Базовая валюта хранится в settings.json под своим именем.
func TestBaseCurrencyJSON(t *testing.T) {
	data, err := json.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"base_currency":"RUB"`) {
		t.Fatalf("в JSON нет base_currency: %s", data)
	}

	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if s.BaseCurrency != CurrencyRUB {
		t.Errorf("BaseCurrency = %q, want %q", s.BaseCurrency, CurrencyRUB)
	}
}

// Файл прежних сборок без base_currency: база остаётся дефолтной (JSON
// накладывается поверх дефолтов).
func TestBaseCurrencyKeepsDefaultWhenAbsent(t *testing.T) {
	s := Default()
	if err := json.Unmarshal([]byte(`{"currencies":{"USD":90}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.BaseCurrency != CurrencyRUB {
		t.Errorf("BaseCurrency = %q, want %q", s.BaseCurrency, CurrencyRUB)
	}
}

// Copy должен переносить таблицу курсов и базовую валюту: черновик диалога
// настроек копирует настройки целиком.
func TestCopyPreservesCurrencyRates(t *testing.T) {
	want := Default()
	want.BaseCurrency = CurrencyUSD

	got, err := want.Copy()
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if got.BaseCurrency != want.BaseCurrency {
		t.Errorf("BaseCurrency = %q, want %q", got.BaseCurrency, want.BaseCurrency)
	}
	if len(got.Currencies) != len(want.Currencies) {
		t.Fatalf("Currencies = %v, want %v", got.Currencies, want.Currencies)
	}
	for cur, rate := range want.Currencies {
		if got.Currencies[cur] != rate {
			t.Errorf("курс %s = %v, want %v", cur, got.Currencies[cur], rate)
		}
	}
}

// Смена базы: курс каждой валюты делится на курс новой базы, сама база — 1.
func TestReanchorCurrencyRates(t *testing.T) {
	s := Default()
	if !s.ReanchorCurrencyRates(CurrencyUSD) {
		t.Fatal("ReanchorCurrencyRates вернул false")
	}

	want := map[Currency]float64{
		CurrencyRUB: 1.0 / 80,
		CurrencyEUR: 88.0 / 80,
		CurrencyUSD: 1,
	}
	for cur, rate := range want {
		if math.Abs(s.Currencies[cur]-rate) > 1e-12 {
			t.Errorf("курс %s = %v, want %v", cur, s.Currencies[cur], rate)
		}
	}
	if s.BaseCurrency != CurrencyUSD {
		t.Errorf("BaseCurrency = %q, want %q", s.BaseCurrency, CurrencyUSD)
	}
}

// Пересчитать нечем: ставки новой базы в карте нет — вызывающий обновляет
// курсы извне, настройки не меняются.
func TestReanchorCurrencyRatesImpossible(t *testing.T) {
	s := Settings{
		BaseCurrency: CurrencyRUB,
		Currencies:   map[Currency]float64{CurrencyRUB: 1},
	}

	if s.ReanchorCurrencyRates(CurrencyUSD) {
		t.Fatal("ReanchorCurrencyRates вернул true, а ставки новой базы нет")
	}
	if s.BaseCurrency != CurrencyRUB {
		t.Errorf("BaseCurrency = %q, want %q", s.BaseCurrency, CurrencyRUB)
	}
	if s.Currencies[CurrencyRUB] != 1 || len(s.Currencies) != 1 {
		t.Errorf("Currencies = %v, want без изменений", s.Currencies)
	}
}

// Сломанная таблица курсов прежних сборок (значения по чужим ключам)
// перебила бы исправленные дефолты: загрузка настроек накладывает JSON
// поверх дефолтов, карта мержится по ключам.
func TestMigrateCurrencyRates(t *testing.T) {
	legacy := map[Currency]float64{
		CurrencyRUB: 80,
		CurrencyEUR: 1.1,
		CurrencyUSD: 1,
	}

	s := Settings{Currencies: legacy}
	s.MigrateCurrencyRates()

	want := Default().Currencies
	for cur, rate := range want {
		if s.Currencies[cur] != rate {
			t.Errorf("курс %s = %v, want %v", cur, s.Currencies[cur], rate)
		}
	}
}

// Пользовательские курсы миграция не трогает.
func TestMigrateCurrencyRatesKeepsCustomRates(t *testing.T) {
	s := Settings{Currencies: map[Currency]float64{
		CurrencyRUB: 1,
		CurrencyUSD: 95,
		CurrencyEUR: 99,
	}}
	s.MigrateCurrencyRates()

	if s.Currencies[CurrencyUSD] != 95 || s.Currencies[CurrencyEUR] != 99 {
		t.Fatalf("Currencies = %v, пользовательские курсы изменены", s.Currencies)
	}
}

// База не RUB, а таблица ещё в рублях — миграция пересчитывает её под базу.
func TestMigrateCurrencyRatesReanchorsToBase(t *testing.T) {
	s := Settings{
		BaseCurrency: CurrencyUSD,
		Currencies:   Default().Currencies,
	}
	s.MigrateCurrencyRates()

	want := map[Currency]float64{
		CurrencyRUB: 1.0 / 80,
		CurrencyEUR: 88.0 / 80,
		CurrencyUSD: 1,
	}
	for cur, rate := range want {
		if math.Abs(s.Currencies[cur]-rate) > 1e-12 {
			t.Errorf("курс %s = %v, want %v", cur, s.Currencies[cur], rate)
		}
	}
}

// Таблица уже в базовой валюте — миграция её не трогает.
func TestMigrateCurrencyRatesKeepsBaseRates(t *testing.T) {
	s := Settings{
		BaseCurrency: CurrencyUSD,
		Currencies: map[Currency]float64{
			CurrencyRUB: 0.0125,
			CurrencyEUR: 1.1,
			CurrencyUSD: 1,
		},
	}
	s.MigrateCurrencyRates()

	want := map[Currency]float64{
		CurrencyRUB: 0.0125,
		CurrencyEUR: 1.1,
		CurrencyUSD: 1,
	}
	for cur, rate := range want {
		if math.Abs(s.Currencies[cur]-rate) > 1e-12 {
			t.Errorf("курс %s = %v, want %v", cur, s.Currencies[cur], rate)
		}
	}
}

// Сломанная таблица прежних сборок в новой базе бессмысленна: миграция
// пересобирает её из дефолтов и пересчитывает под базу.
func TestMigrateCurrencyRatesRebuildsLegacyTableForNewBase(t *testing.T) {
	s := Settings{
		BaseCurrency: CurrencyUSD,
		Currencies: map[Currency]float64{
			CurrencyRUB: 80,
			CurrencyEUR: 1.1,
			CurrencyUSD: 1,
		},
	}
	s.MigrateCurrencyRates()

	want := map[Currency]float64{
		CurrencyRUB: 1.0 / 80,
		CurrencyEUR: 88.0 / 80,
		CurrencyUSD: 1,
	}
	for cur, rate := range want {
		if math.Abs(s.Currencies[cur]-rate) > 1e-12 {
			t.Errorf("курс %s = %v, want %v", cur, s.Currencies[cur], rate)
		}
	}
}
