// Тесты начисления по правилам Twitch (e11s02-t2).
//
// Тесты не зависят от сети: проверяется чистая часть пайплайна — правило из
// настроек, начисление по тиру, начисление ре-саба и запись в журнал
// операций. Сам поток событий (StartEvents) проверяется в internal/providers.
package app

import (
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/timer"
)

// twitchTestConfig — правило сабов: Tier 1 — минута, Tier 2 — пять минут,
// Tier 3 — десять минут.
func twitchTestConfig() settings.TwitchConfig {
	return settings.TwitchConfig{
		Tier1SubTime: settings.TimeSettings{Value: 1, Unit: settings.TimeUnitMinute},
		Tier2SubTime: settings.TimeSettings{Value: 5, Unit: settings.TimeUnitMinute},
		Tier3SubTime: settings.TimeSettings{Value: 10, Unit: settings.TimeUnitMinute},
		CountResub:   true,
	}
}

// twitchRule — правило сабов по указанным настройкам Twitch.
func twitchRule(cfg settings.TwitchConfig) rules.Rule {
	s := testSettings()
	s.Providers.Twitch = cfg

	return ruleFromSettings(s)
}

// TestSubscriptionAccruesByTier — саб Tier 1000 начисляется по правилу
// Tier 1 (минута), в журнале запись с источником twitch.
func TestSubscriptionAccruesByTier(t *testing.T) {
	tm := timer.New(time.Minute)
	rule := twitchRule(twitchTestConfig())

	applySubscription(tm, rule, providers.TwitchSubscriptionEvent{Tier: "1000"})

	if got, want := tm.Remaining(), 2*time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s", got, want)
	}

	entry := lastAuditEntry(t, tm)
	if entry.Op != timer.OpAdd {
		t.Fatalf("операция журнала: %q, ожидалось %q", entry.Op, timer.OpAdd)
	}
	if entry.Source != timer.SourceTwitch {
		t.Fatalf("источник журнала: %q, ожидалось %q", entry.Source, timer.SourceTwitch)
	}
	if want := "подписка Tier 1000"; entry.Detail != want {
		t.Fatalf("деталь журнала: %q, ожидалось %q", entry.Detail, want)
	}
}

// TestSubscriptionTierRules — тиры 2000 и 3000 считаются по своим правилам.
func TestSubscriptionTierRules(t *testing.T) {
	cases := []struct {
		tier string
		want time.Duration
	}{
		{tier: "2000", want: 5 * time.Minute},
		{tier: "3000", want: 10 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.tier, func(t *testing.T) {
			tm := timer.New(time.Minute)
			rule := twitchRule(twitchTestConfig())

			applySubscription(tm, rule, providers.TwitchSubscriptionEvent{Tier: tc.tier})

			if got, want := tm.Remaining(), time.Minute+tc.want; got != want {
				t.Fatalf("остаток таймера: %s, ожидалось %s", got, want)
			}
		})
	}
}

// TestSubscriptionUnknownTier — неизвестный тир (prime и т.п.): начисления
// нет, причина — в консоль.
func TestSubscriptionUnknownTier(t *testing.T) {
	tm := timer.New(time.Minute)
	rule := twitchRule(twitchTestConfig())

	applySubscription(tm, rule, providers.TwitchSubscriptionEvent{Tier: "prime"})

	if got, want := tm.Remaining(), time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s (начисления быть не должно)", got, want)
	}
	if audit := tm.Audit(); len(audit) != 0 {
		t.Fatalf("журнал операций: %d записей, ожидалось 0", len(audit))
	}
}

// TestResubNotCounted — при CountResub=false ре-сабы не начисляются:
// ни секунд, ни записи в журнале.
func TestResubNotCounted(t *testing.T) {
	tm := timer.New(time.Minute)

	cfg := twitchTestConfig()
	cfg.CountResub = false
	rule := twitchRule(cfg)

	applySubscription(tm, rule, providers.TwitchSubscriptionEvent{
		Tier:             "1000",
		Resub:            true,
		CumulativeMonths: 3,
	})

	if got, want := tm.Remaining(), time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s (начисления быть не должно)", got, want)
	}
	if audit := tm.Audit(); len(audit) != 0 {
		t.Fatalf("журнал операций: %d записей, ожидалось 0", len(audit))
	}
}

// TestResubCountedByTier — при CountResub=true ре-саб считается по правилу
// своего тира, в журнале — «ре-саб Tier 2000 (3 мес)».
func TestResubCountedByTier(t *testing.T) {
	cases := []struct {
		tier string
		want time.Duration
	}{
		{tier: "1000", want: time.Minute},
		{tier: "2000", want: 5 * time.Minute},
		{tier: "3000", want: 10 * time.Minute},
	}

	for _, tc := range cases {
		t.Run(tc.tier, func(t *testing.T) {
			tm := timer.New(time.Minute)

			cfg := twitchTestConfig()
			cfg.CountResub = true
			rule := twitchRule(cfg)

			applySubscription(tm, rule, providers.TwitchSubscriptionEvent{
				Tier:             tc.tier,
				Resub:            true,
				CumulativeMonths: 3,
			})

			if got, want := tm.Remaining(), time.Minute+tc.want; got != want {
				t.Fatalf("остаток таймера: %s, ожидалось %s", got, want)
			}

			entry := lastAuditEntry(t, tm)
			if entry.Op != timer.OpAdd {
				t.Fatalf("операция журнала: %q, ожидалось %q", entry.Op, timer.OpAdd)
			}
			if entry.Source != timer.SourceTwitch {
				t.Fatalf("источник журнала: %q, ожидалось %q", entry.Source, timer.SourceTwitch)
			}
			if want := "ре-саб Tier " + tc.tier + " (3 мес)"; entry.Detail != want {
				t.Fatalf("деталь журнала: %q, ожидалось %q", entry.Detail, want)
			}
		})
	}
}

// TestResubUnknownTier — ре-саб неизвестного тира: начисления нет.
func TestResubUnknownTier(t *testing.T) {
	tm := timer.New(time.Minute)

	cfg := twitchTestConfig()
	cfg.CountResub = true
	rule := twitchRule(cfg)

	applySubscription(tm, rule, providers.TwitchSubscriptionEvent{
		Tier:             "prime",
		Resub:            true,
		CumulativeMonths: 3,
	})

	if got, want := tm.Remaining(), time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s (начисления быть не должно)", got, want)
	}
	if audit := tm.Audit(); len(audit) != 0 {
		t.Fatalf("журнал операций: %d записей, ожидалось 0", len(audit))
	}
}

// TestSubscriptionHandlerRoutesEvents — обработчик реагирует только на
// подписки: донат и неизвестный тип данных игнорируются.
func TestSubscriptionHandlerRoutesEvents(t *testing.T) {
	tm := timer.New(time.Minute)
	handler := subscriptionHandler(tm, rules.NewStore(twitchRule(twitchTestConfig())))

	handler(providers.EventTypeDonation, providers.TwitchSubscriptionEvent{Tier: "1000"})
	handler(providers.EventTypeSubscription, "какая-то строка")
	handler(providers.EventTypeSubscription, (*providers.TwitchSubscriptionEvent)(nil))

	if got, want := tm.Remaining(), time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s (начисления быть не должно)", got, want)
	}

	handler(providers.EventTypeSubscription, providers.TwitchSubscriptionEvent{Tier: "1000"})

	if got, want := tm.Remaining(), 2*time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s", got, want)
	}
}

// TestSubscriptionHandlerSkipsResub — обработчик при CountResub=false
// ре-сабы пропускает, обычные сабы считает.
func TestSubscriptionHandlerSkipsResub(t *testing.T) {
	tm := timer.New(time.Minute)

	cfg := twitchTestConfig()
	cfg.CountResub = false
	handler := subscriptionHandler(tm, rules.NewStore(twitchRule(cfg)))

	handler(providers.EventTypeSubscription, providers.TwitchSubscriptionEvent{
		Tier:             "2000",
		Resub:            true,
		CumulativeMonths: 3,
	})

	if got, want := tm.Remaining(), time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s (ре-саб не считается)", got, want)
	}

	handler(providers.EventTypeSubscription, providers.TwitchSubscriptionEvent{Tier: "2000"})

	if got, want := tm.Remaining(), time.Minute+5*time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s", got, want)
	}
}

// TestSubRuleFromSettings — правило собирается из настроек: секунды за тир.
func TestSubRuleFromSettings(t *testing.T) {
	cfg := twitchTestConfig()
	rule := twitchRule(cfg)

	if got, want := rule.Sub.Tier1000, int64(60); got != want {
		t.Fatalf("Tier1000: %d, ожидалось %d", got, want)
	}
	if got, want := rule.Sub.Tier2000, int64(300); got != want {
		t.Fatalf("Tier2000: %d, ожидалось %d", got, want)
	}
	if got, want := rule.Sub.Tier3000, int64(600); got != want {
		t.Fatalf("Tier3000: %d, ожидалось %d", got, want)
	}
}
