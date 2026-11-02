// Начисление по правилам DonationAlerts.
//
// Правила начисления живут в internal/rules — это канонический источник:
// здесь формулы не переписываются, а вызываются (ToBase, DonationSeconds).
// Запись в журнал операций делает сам Timer.Add — отдельного вызова не нужно.
// Подробности — references/twitch-events.md.
package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
)

// startDonationAlertsEvents подписывается на события DonationAlerts и
// запускает поток событий.
//
// Таймер берётся из состояния окна на frame thread (в начале функции):
// *AppState из фона трогать нельзя, а указатель на движок потокобезопасен.
// Стор правил передаётся в обработчик: он читается в момент события, поэтому
// смена настроек действует без перезапуска потока.
func startDonationAlertsEvents(
	w *gui.Window,
	p *providers.DonationAlertsProvider,
	store *rules.Store,
) {
	st := gui.State[state.AppState](w)
	tm := st.Timer

	// Подписка до старта потока: иначе донаты, пришедшие сразу после
	// подключения, прошли бы мимо.
	p.On(donationHandler(tm, store))

	// Контекст потока событий отменяется вместе с окном. Когда провайдера
	// останавливают (StopEvents), его StartEvents возвращается, и горутина
	// завершается сама.
	ctx, cancel := context.WithCancel(w.Ctx())

	go func() {
		defer cancel()
		if err := p.StartEvents(ctx); err != nil {
			fmt.Println("donationalerts: поток событий:", err)
		}
	}()
}

// donationHandler — обработчик событий DonationAlerts. Живой тип события —
// донат; неизвестные типы игнорируются.
//
// Правило берётся из стора в момент события: настройки, сохранённые после
// старта потока, применяются к следующим донатам.
func donationHandler(tm *timer.Timer, store *rules.Store) providers.EventHandler {
	return func(eventType providers.EventType, data interface{}) {
		if eventType != providers.EventTypeDonation {
			return
		}

		var ev providers.DonationEvent
		switch v := data.(type) {
		case providers.DonationEvent:
			ev = v
		case *providers.DonationEvent:
			if v == nil {
				return
			}
			ev = *v
		default:
			fmt.Printf("donationalerts: неизвестные данные события: %T\n", data)
			return
		}

		applyDonation(tm, store.Get(), ev)
	}
}

// applyDonation — начисление за один донат: сумма в базовой валюте →
// секунды по правилу → Timer.Add. Неизвестная валюта — сообщение в консоль.
func applyDonation(tm *timer.Timer, rule rules.Rule, ev providers.DonationEvent) {
	d, err := donationSeconds(rule, ev)
	if err != nil {
		fmt.Println("donationalerts: донат не начислен:", err)
		return
	}
	if d <= 0 {
		return
	}

	tm.Add(timer.SourceDonationalerts, d, donationDetail(ev))
}

// donationSeconds — чистый расчёт начисления за донат: сумма в базовой
// валюте по курсу → секунды по правилу. Нулевая сумма даёт 0 секунд,
// неизвестная валюта — ошибку.
func donationSeconds(rule rules.Rule, ev providers.DonationEvent) (time.Duration, error) {
	base, err := rule.ToBase(ev.Amount, ev.Currency)
	if err != nil {
		return 0, err
	}

	return rule.DonationSeconds(base), nil
}

// donationDetail — человекочитаемая запись для журнала: «донат Ivan (500 RUB)».
func donationDetail(ev providers.DonationEvent) string {
	name := strings.TrimSpace(ev.Username)
	if name == "" {
		name = "аноним"
	}

	amount := strconv.FormatFloat(ev.Amount, 'f', -1, 64)

	currency := strings.TrimSpace(ev.Currency)
	if currency == "" {
		return "донат " + name + " (" + amount + ")"
	}

	return "донат " + name + " (" + amount + " " + currency + ")"
}
