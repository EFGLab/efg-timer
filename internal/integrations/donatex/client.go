// Package donatex — интеграция Donatex (e03s01): SignalR-клиент публичного
// хаба донатов donatex.gg. Событие DonationCreated передаётся обработчику;
// DonationRerun (повторный показ доната) не начисляется — метод не объявлен,
// библиотека отвечает completion-ошибкой, соединение живёт.
// Переподключение автоматическое, с лимитом не более 2 подключений в секунду.
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

// defaultHubURL — публичный хаб донатов Donatex (референс: виджет ODA,
// PaymentAlerts.tsx: токен в query-параметре access_token).
const defaultHubURL = "https://donatex.gg/api/public-donations-hub"

// reconnectInterval — минимальная пауза между подключениями; 500 мс даёт
// верхний предел 2 подключения/сек (требование e03s01-t2).
const reconnectInterval = 500 * time.Millisecond

// Donation — payload события DonationCreated; имена полей camelCase,
// как на проводе. Проверочные донаты (isTest) тоже начисляются (t3).
type Donation struct {
	ID          string  `json:"id"`
	Amount      float64 `json:"amount"`
	Currency    string  `json:"currency"`
	AmountInRub float64 `json:"amountInRub"`
	Username    string  `json:"username"`
	Message     string  `json:"message"`
	IsTest      bool    `json:"isTest"`
}

// Handler вызывается на каждый DonationCreated (из горутины библиотеки).
type Handler func(Donation)

// Client — подключение к хабу Donatex; Run блокируется до отмены ctx.
type Client struct {
	hubURL     string // включает access_token в query
	onDonation Handler
	slots      *rateLimiter
}

// New — клиент реального хаба donatex.gg.
func New(token string, onDonation Handler) *Client {
	return newClient(defaultHubURL, token, onDonation)
}

// newClient — клиент произвольного хаба (тесты поднимают mock).
func newClient(hubURL, token string, onDonation Handler) *Client {
	return &Client{
		hubURL:     hubURL + "?access_token=" + url.QueryEscape(token),
		onDonation: onDonation,
		slots:      newRateLimiter(reconnectInterval, time.Now),
	}
}

// Run подключается и живёт до отмены ctx. Переподключения внутренние:
// библиотечный backoff — постоянная секунда (неистощаемый), а каждый
// коннект (первый и каждый переподключение) проходит через лимитер ≤2/сек.
func (c *Client) Run(ctx context.Context) error {
	// Фабрика соединений библиотеки — без ctx: захватываем его замыканием;
	// при отмене ctx и wait, и HttpConnectionFactory возвращают ошибку.
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

// receiver — принимаемые методы хаба (server-to-client).
type receiver struct {
	on Handler
}

// DonationCreated — единственное начисляющее событие хаба.
func (r *receiver) DonationCreated(d Donation) {
	if r.on != nil {
		r.on(d)
	}
}

// DonationRerun намеренно не объявлен: библиотека отвечает
// completion-ошибкой «Unknown method» и продолжает — повтор не начисляется.

// rateLimiter допускает подключения не чаще одного раза за interval.
// reserve — чистая функция времени (тестируется на подставных часах);
// wait — блокирующая обёртка с отменой по ctx.
type rateLimiter struct {
	interval time.Duration
	now      func() time.Time

	mu   sync.Mutex
	last time.Time // последнее захваченное разрешение
}

func newRateLimiter(interval time.Duration, now func() time.Time) *rateLimiter {
	return &rateLimiter{interval: interval, now: now}
}

// reserve возвращает 0, если слот захвачен сейчас, иначе — сколько ждать
// до ближайшего разрешённого подключения.
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

// wait блокирует до получения слота или отмены ctx.
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
