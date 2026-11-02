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

func startDonationAlertsEvents(
	w *gui.Window,
	p *providers.DonationAlertsProvider,
	store *rules.Store,
) {
	st := gui.State[state.AppState](w)
	tm := st.Timer

	p.On(donationHandler(tm, store))

	ctx, cancel := context.WithCancel(w.Ctx())

	go func() {
		defer cancel()
		if err := p.StartEvents(ctx); err != nil {
			fmt.Println("donationalerts: поток событий:", err)
		}
	}()
}

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

func donationSeconds(rule rules.Rule, ev providers.DonationEvent) (time.Duration, error) {
	base, err := rule.ToBase(ev.Amount, ev.Currency)
	if err != nil {
		return 0, err
	}

	return rule.DonationSeconds(base), nil
}

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
