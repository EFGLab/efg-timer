// Начисление по правилам Twitch (e11s02-t2).
//
// События провайдера (подписки и ре-сабы) приходят в обработчик, подписанный
// через p.On: по тиру саба берётся фиксированное число секунд из правил
// (internal/rules), они добавляются таймеру.
//
// Запись в журнал операций делает сам Timer.Add (кольцевой буфер на 200
// записей) — отдельного вызова не нужно.
package app

import (
	"context"
	"fmt"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
)

// startTwitchEvents подписывается на события Twitch и запускает поток
// событий.
//
// Таймер берётся из состояния окна на frame thread (в начале функции):
// *AppState из фона трогать нельзя, а указатель на движок потокобезопасен —
// его можно использовать из горутины событий.
//
// Стор правил передаётся в обработчик: он читается в момент события, поэтому
// смена настроек действует без перезапуска потока.
func startTwitchEvents(w *gui.Window, p *providers.TwitchProvider, store *rules.Store) {
	st := gui.State[state.AppState](w)
	tm := st.Timer
	cfg := st.Settings.Providers.Twitch

	// Подписка до старта потока: иначе подписки, пришедшие сразу после
	// подключения, прошли бы мимо.
	p.On(subscriptionHandler(tm, store))

	// Контекст потока событий отменяется вместе с окном. Когда провайдера
	// останавливают (StopEvents), его StartEvents возвращается, и горутина
	// завершается сама.
	ctx, cancel := context.WithCancel(w.Ctx())

	go func() {
		defer cancel()
		// broadcasterID — id канала владельца из настроек (cfg.UserID).
		if err := p.StartEvents(ctx, cfg.UserID); err != nil {
			fmt.Println("twitch: поток событий:", err)
		}
	}()
}

// subscriptionHandler — обработчик событий Twitch. Живой тип события —
// подписка (саб или ре-саб); неизвестные типы игнорируются.
//
// Правило берётся из стора в момент события: настройки, сохранённые после
// старта потока, применяются к следующим подпискам.
func subscriptionHandler(tm *timer.Timer, store *rules.Store) providers.EventHandler {
	return func(eventType providers.EventType, data interface{}) {
		if eventType != providers.EventTypeSubscription {
			return
		}

		var ev providers.TwitchSubscriptionEvent
		switch v := data.(type) {
		case providers.TwitchSubscriptionEvent:
			ev = v
		case *providers.TwitchSubscriptionEvent:
			if v == nil {
				return
			}
			ev = *v
		default:
			fmt.Printf("twitch: неизвестные данные события: %T\n", data)
			return
		}

		applySubscription(tm, store.Get(), ev)
	}
}

// applySubscription — начисление за подписку: секунды по тиру → Timer.Add.
// Ре-саб при выключенном CountResub не начисляется; неизвестный тир (prime
// и т.п.) — сообщение в консоль, начисления нет.
func applySubscription(tm *timer.Timer, rule rules.Rule, ev providers.TwitchSubscriptionEvent) {
	if ev.Resub && !rule.Sub.CountResub {
		return
	}

	d, err := rule.SubSeconds(ev.Tier)
	if err != nil {
		fmt.Println("twitch: подписка не начислена:", err)
		return
	}
	if d <= 0 {
		return
	}

	if ev.Resub {
		tm.Add(timer.SourceTwitch, d, fmt.Sprintf("ре-саб Tier %s (%d мес)", ev.Tier, ev.CumulativeMonths))
		return
	}

	tm.Add(timer.SourceTwitch, d, "подписка Tier "+ev.Tier)
}
