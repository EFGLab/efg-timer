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

func startTwitchEvents(w *gui.Window, p *providers.TwitchProvider, store *rules.Store) {
	st := gui.State[state.AppState](w)
	tm := st.Timer
	cfg := st.Settings.Providers.Twitch

	p.On(subscriptionHandler(tm, store))

	ctx, cancel := context.WithCancel(w.Ctx())

	go func() {
		defer cancel()
		if err := p.StartEvents(ctx, cfg.UserID); err != nil {
			fmt.Println("twitch: поток событий:", err)
		}
	}()
}

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
