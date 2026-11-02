package donationalerts

import (
	"context"
	"time"
)

func RunDonations(ctx context.Context, opts CentrifugoOptions, applier *DonationApplier, sink func(time.Duration), onError func(error)) error {
	donations := make(chan DonationEvent, 16)
	opts.OnDonation = func(ev DonationEvent) {
		select {
		case donations <- ev:
		default:
		}
	}
	done := make(chan error, 1)
	go func() { done <- NewCentrifugo(opts).Run(ctx) }()
	for {
		select {
		case <-ctx.Done():
			<-done
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
