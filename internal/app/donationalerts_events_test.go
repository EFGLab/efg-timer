// Тесты начисления по правилам DonationAlerts.
//
// Тесты не зависят от сети: проверяется чистая часть пайплайна — правило из
// настроек, перевод суммы в рубли, начисление и запись в журнал операций.
// Сам поток событий (StartEvents) проверяется в internal/providers.
package app

import (
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/timer"
)

// daTestRule — правило донатов из настроек теста: «1 минута за каждые 100
// RUB» с курсами (RUB проходит без курса — это делает сам internal/rules).
func daTestRule() rules.Rule {
	return ruleFromSettings(testSettings())
}

// lastAuditEntry — последняя запись журнала операций.
func lastAuditEntry(t *testing.T, tm *timer.Timer) timer.AuditEntry {
	t.Helper()

	audit := tm.Audit()
	if len(audit) == 0 {
		t.Fatal("журнал операций пуст, а запись ожидалась")
	}

	return audit[len(audit)-1]
}

// TestDonationAccruesByRule — донат 500 RUB при правиле «1 минута за 100»:
// в таймере +5 минут, в журнале запись с источником donationalerts.
func TestDonationAccruesByRule(t *testing.T) {
	tm := timer.New(time.Minute)
	rule := daTestRule()

	applyDonation(tm, rule, providers.DonationEvent{
		ID:       1,
		Username: "Ivan",
		Amount:   500,
		Currency: "RUB",
	})

	if got, want := tm.Remaining(), 6*time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s", got, want)
	}

	entry := lastAuditEntry(t, tm)
	if entry.Op != timer.OpAdd {
		t.Fatalf("операция журнала: %q, ожидалось %q", entry.Op, timer.OpAdd)
	}
	if entry.Source != timer.SourceDonationalerts {
		t.Fatalf("источник журнала: %q, ожидалось %q", entry.Source, timer.SourceDonationalerts)
	}
	if want := "донат Ivan (500 RUB)"; entry.Detail != want {
		t.Fatalf("деталь журнала: %q, ожидалось %q", entry.Detail, want)
	}
}

// TestDonationUnknownCurrency — донат в валюте без курса: начисления нет.
func TestDonationUnknownCurrency(t *testing.T) {
	tm := timer.New(time.Minute)
	rule := daTestRule()

	applyDonation(tm, rule, providers.DonationEvent{
		ID:       2,
		Username: "Ivan",
		Amount:   500,
		Currency: "JPY",
	})

	if got, want := tm.Remaining(), time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s (начисления быть не должно)", got, want)
	}
	if audit := tm.Audit(); len(audit) != 0 {
		t.Fatalf("журнал операций: %d записей, ожидалось 0", len(audit))
	}
}

// TestDonationZeroAmount — нулевая сумма: начисления нет.
func TestDonationZeroAmount(t *testing.T) {
	tm := timer.New(time.Minute)
	rule := daTestRule()

	applyDonation(tm, rule, providers.DonationEvent{
		ID:       3,
		Username: "Ivan",
		Amount:   0,
		Currency: "RUB",
	})

	if got, want := tm.Remaining(), time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s (начисления быть не должно)", got, want)
	}
	if audit := tm.Audit(); len(audit) != 0 {
		t.Fatalf("журнал операций: %d записей, ожидалось 0", len(audit))
	}
}

// TestDonationCurrencyRate — донат в валюте с курсом: сумма переводится в
// рубли по курсу из настроек (5 USD * 90 = 450 RUB → 4 мин 30 с).
func TestDonationCurrencyRate(t *testing.T) {
	tm := timer.New(time.Minute)
	rule := daTestRule()

	applyDonation(tm, rule, providers.DonationEvent{
		ID:       4,
		Username: "Ivan",
		Amount:   5,
		Currency: "USD",
	})

	if got, want := tm.Remaining(), time.Minute+4*time.Minute+30*time.Second; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s", got, want)
	}

	entry := lastAuditEntry(t, tm)
	if want := "донат Ivan (5 USD)"; entry.Detail != want {
		t.Fatalf("деталь журнала: %q, ожидалось %q", entry.Detail, want)
	}
}

// TestDonationHandlerRoutesEvents — обработчик реагирует только на донаты:
// подписка и неизвестный тип данных игнорируются.
func TestDonationHandlerRoutesEvents(t *testing.T) {
	tm := timer.New(time.Minute)
	handler := donationHandler(tm, rules.NewStore(daTestRule()))

	handler(providers.EventTypeSubscription, providers.DonationEvent{Amount: 500, Currency: "RUB"})
	handler(providers.EventTypeDonation, "какая-то строка")
	handler(providers.EventTypeDonation, (*providers.DonationEvent)(nil))

	if got, want := tm.Remaining(), time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s (начисления быть не должно)", got, want)
	}

	handler(providers.EventTypeDonation, providers.DonationEvent{
		ID:       5,
		Username: "Ivan",
		Amount:   100,
		Currency: "RUB",
	})

	if got, want := tm.Remaining(), 2*time.Minute; got != want {
		t.Fatalf("остаток таймера: %s, ожидалось %s", got, want)
	}
}

// TestDonationDetail — человекочитаемая деталь журнала.
func TestDonationDetail(t *testing.T) {
	cases := []struct {
		name string
		ev   providers.DonationEvent
		want string
	}{
		{
			name: "обычный донат",
			ev:   providers.DonationEvent{Username: "Ivan", Amount: 500, Currency: "RUB"},
			want: "донат Ivan (500 RUB)",
		},
		{
			name: "дробная сумма",
			ev:   providers.DonationEvent{Username: "Ivan", Amount: 500.5, Currency: "RUB"},
			want: "донат Ivan (500.5 RUB)",
		},
		{
			name: "без имени",
			ev:   providers.DonationEvent{Amount: 100, Currency: "RUB"},
			want: "донат аноним (100 RUB)",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := donationDetail(tc.ev); got != tc.want {
				t.Fatalf("деталь: %q, ожидалось %q", got, tc.want)
			}
		})
	}
}
