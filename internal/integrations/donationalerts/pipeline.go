package donationalerts

import (
	"context"
	"time"
)

// RunDonations — склейка e05s02-t3: публикации Centrifugo → DonationApplier
// (RUB-конверсия + дедуп по id) → sink (main добавляет секунды таймеру).
// Валютные ошибки сообщаются в onError (nil — молча). Блокируется до
// ctx.Done (возвращает nil) или до ошибки соединения.
func RunDonations(ctx context.Context, opts CentrifugoOptions, applier *DonationApplier, sink func(time.Duration), onError func(error)) error {
	donations := make(chan DonationEvent, 16)
	opts.OnDonation = func(ev DonationEvent) {
		select {
		case donations <- ev:
		default: // переполнение — дроп; повторы погасит дедуп applier'а
		}
	}
	done := make(chan error, 1)
	go func() { done <- NewCentrifugo(opts).Run(ctx) }()
	for {
		select {
		case <-ctx.Done():
			<-done // дождаться завершения чтения (nil после отмены)
			return nil
		case err := <-done:
			return err
		case ev := <-donations:
			d, err := applier.Apply(ev)
			if err != nil {
				if onError != nil {
					onError(err)
				}
				continue
			}
			if d > 0 && sink != nil {
				sink(d)
			}
		}
	}
}
