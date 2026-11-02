// Тесты живых правил начисления: сборка из настроек, обновление в сторе и
// применение новых правил к следующим событиям.
package app

import (
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
)

// testSettings — настройки для тестов правил: донат «1 минута за каждые
// 100 RUB», тиры сабов 1/5/10 минут, база RUB, курс USD = 90.
func testSettings() settings.Settings {
	s := settings.Default()
	s.BaseCurrency = settings.CurrencyRUB
	s.Providers.DonationAlerts.Time = settings.TimeSettings{Value: 1, Unit: settings.TimeUnitMinute}
	s.Providers.DonationAlerts.PerAmount = 100
	s.Providers.Twitch.Tier1SubTime = settings.TimeSettings{Value: 1, Unit: settings.TimeUnitMinute}
	s.Providers.Twitch.Tier2SubTime = settings.TimeSettings{Value: 5, Unit: settings.TimeUnitMinute}
	s.Providers.Twitch.Tier3SubTime = settings.TimeSettings{Value: 10, Unit: settings.TimeUnitMinute}
	s.Currencies = map[settings.Currency]float64{
		settings.CurrencyRUB: 1,
		settings.CurrencyUSD: 90,
	}
	return s
}

// TestRuleFromSettings — правило собирается из настроек: донат, тиры
// сабов, базовая валюта и курсы (ключи курсов — строковые).
func TestRuleFromSettings(t *testing.T) {
	rule := ruleFromSettings(testSettings())

	if got, want := rule.Donation.SecondsPerRub, 60.0; got != want {
		t.Fatalf("SecondsPerRub: %v, ожидалось %v", got, want)
	}
	if got, want := rule.Donation.PerAmount, 100.0; got != want {
		t.Fatalf("PerAmount: %v, ожидалось %v", got, want)
	}
	if got, want := rule.Sub.Tier1000, int64(60); got != want {
		t.Fatalf("Tier1000: %d, ожидалось %d", got, want)
	}
	if got, want := rule.Sub.Tier2000, int64(300); got != want {
		t.Fatalf("Tier2000: %d, ожидалось %d", got, want)
	}
	if got, want := rule.Sub.Tier3000, int64(600); got != want {
		t.Fatalf("Tier3000: %d, ожидалось %d", got, want)
	}
	if got, want := rule.Base, "RUB"; got != want {
		t.Fatalf("база валюты: %q, ожидалось %q", got, want)
	}
	if got, want := rule.Currencies["USD"], 90.0; got != want {
		t.Fatalf("курс USD: %v, ожидалось %v", got, want)
	}
	if got, want := rule.Currencies["RUB"], 1.0; got != want {
		t.Fatalf("курс RUB: %v, ожидалось %v", got, want)
	}
}

// TestApplyRulesToStore — после смены настроек стор отдаёт новое правило.
func TestApplyRulesToStore(t *testing.T) {
	st := state.InitialAppState()
	st.Settings = testSettings()
	applyRulesToStore(st)

	st.Settings.Providers.DonationAlerts.Time = settings.TimeSettings{Value: 2, Unit: settings.TimeUnitMinute}
	st.Settings.Providers.Twitch.Tier1SubTime = settings.TimeSettings{Value: 3, Unit: settings.TimeUnitMinute}
	applyRulesToStore(st)

	rule := st.Rules.Get()
	if got, want := rule.Donation.SecondsPerRub, 120.0; got != want {
		t.Fatalf("SecondsPerRub после смены настроек: %v, ожидалось %v", got, want)
	}
	if got, want := rule.Sub.Tier1000, int64(180); got != want {
		t.Fatalf("Tier1000 после смены настроек: %d, ожидалось %d", got, want)
	}
}

// TestDonationHandlerUsesLiveRule — главный кейс: правило в сторе меняется
// между событиями, второе событие считается по новому правилу.
func TestDonationHandlerUsesLiveRule(t *testing.T) {
	tm := timer.New(time.Minute)
	store := rules.NewStore(ruleFromSettings(testSettings()))
	handler := donationHandler(tm, store)

	handler(providers.EventTypeDonation, providers.DonationEvent{
		ID:       1,
		Username: "Ivan",
		Amount:   100,
		Currency: "RUB",
	})

	if got, want := tm.Remaining(), 2*time.Minute; got != want {
		t.Fatalf("остаток таймера после первого доната: %s, ожидалось %s", got, want)
	}

	// Правило меняется на лету: 1 секунда за каждые 100.
	changed := testSettings()
	changed.Providers.DonationAlerts.Time = settings.TimeSettings{Value: 1, Unit: settings.TimeUnitSecond}
	store.Set(ruleFromSettings(changed))

	handler(providers.EventTypeDonation, providers.DonationEvent{
		ID:       2,
		Username: "Ivan",
		Amount:   100,
		Currency: "RUB",
	})

	if got, want := tm.Remaining(), 2*time.Minute+time.Second; got != want {
		t.Fatalf("остаток таймера после второго доната: %s, ожидалось %s", got, want)
	}
}

// TestSubscriptionHandlerUsesLiveRule — правило сабов тоже читается в момент
// события: смена правила между событиями меняет начисление.
func TestSubscriptionHandlerUsesLiveRule(t *testing.T) {
	tm := timer.New(time.Minute)
	store := rules.NewStore(ruleFromSettings(testSettings()))
	handler := subscriptionHandler(tm, store)

	handler(providers.EventTypeSubscription, providers.TwitchSubscriptionEvent{Tier: "1000"})

	if got, want := tm.Remaining(), 2*time.Minute; got != want {
		t.Fatalf("остаток таймера после первой подписки: %s, ожидалось %s", got, want)
	}

	// Тир 1000 теперь даёт 5 минут вместо минуты.
	changed := testSettings()
	changed.Providers.Twitch.Tier1SubTime = settings.TimeSettings{Value: 5, Unit: settings.TimeUnitMinute}
	store.Set(ruleFromSettings(changed))

	handler(providers.EventTypeSubscription, providers.TwitchSubscriptionEvent{Tier: "1000"})

	if got, want := tm.Remaining(), 7*time.Minute; got != want {
		t.Fatalf("остаток таймера после второй подписки: %s, ожидалось %s", got, want)
	}
}
