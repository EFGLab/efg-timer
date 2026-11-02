package donatex

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/philippseith/signalr"
)

const defaultHubURL = "https://donatex.gg/api/public-donations-hub"

const reconnectInterval = 500 * time.Millisecond

type Donation struct {
	ID          string  `json:"id"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	AmountInRub float64 `json:"amountInRub"`
	Username    string  `json:"username"`
	Message     string  `json:"message"`
	IsTest      bool    `json:"isTest"`
}

type Handler func(Donation)

type Client struct {
	hubURL     string
	onDonation Handler
	slots      *rateLimiter
}

func New(token string, onDonation Handler) *Client {
	return newClient(defaultHubURL, token, onDonation)
}

func newClient(hubURL, token string, onDonation Handler) *Client {
	return &Client{
		hubURL:     hubURL + "?access_token=" + url.QueryEscape(token),
		onDonation: onDonation,
		slots:      newRateLimiter(reconnectInterval, time.Now),
	}
}

func (c *Client) Run(ctx context.Context) error {
	connect := func() (signalr.Connection, error) {
		if err := c.slots.wait(ctx); err != nil {
			return nil, err
		}
		return signalr.HttpConnectionFactory(ctx, c.hubURL)
	}
	cl, err := signalr.NewClient(ctx,
		signalr.WithConnector(connect),
		signalr.WithReceiver(&receiver{on: c.onDonation}),
		signalr.WithBackoff(func() backoff.BackOff {
			return backoff.NewConstantBackOff(time.Second)
		}),
	)
	if err != nil {
		return fmt.Errorf("signalr клиент: %w", err)
	}
	cl.Start()
	select {
	case err := <-cl.WaitForState(ctx, signalr.ClientClosed):
		if err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	case <-ctx.Done():
		cl.Stop()
		return nil
	}
}

type receiver struct {
	on Handler
}

func (r *receiver) DonationCreated(d Donation) {
	if r.on != nil {
		r.on(d)
	}
}

type rateLimiter struct {
	interval time.Duration
	now      func() time.Time

	mu   sync.Mutex
	last time.Time
}

func newRateLimiter(interval time.Duration, now func() time.Time) *rateLimiter {
	return &rateLimiter{interval: interval, now: now}
}

func (l *rateLimiter) reserve() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	eligible := l.last.Add(l.interval)
	if l.last.IsZero() || !now.Before(eligible) {
		l.last = now
		return 0
	}
	l.last = eligible
	return eligible.Sub(now)
}

func (l *rateLimiter) wait(ctx context.Context) error {
	for {
		d := l.reserve()
		if d == 0 {
			return nil
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}
