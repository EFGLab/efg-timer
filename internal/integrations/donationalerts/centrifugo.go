package donationalerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// DefaultCentrifugoURL — WS-эндпоинт DA. Сервер говорит JSON-протокол
// Centrifugo v1 (эпохи Centrifugo v2): в доки DA примеры сообщений
// {"method":1,"id":2,"params":{...}} — поэтому современный centrifuge-go
// (protobuf v2) здесь не применим, клиент реализован на gorilla/websocket.
const DefaultCentrifugoURL = "wss://centrifugo.donationalerts.com/connection/websocket"

// DonationEvent — донат из публикации канала $alerts:donation_<user_id>;
// поля лежат во вложенном data.data, amount в исходной валюте.
type DonationEvent struct {
	ID       int64   `json:"id"`
	Username string  `json:"username"`
	Message  string  `json:"message"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// CentrifugoOptions — параметры DA-соединения. Client поставляет Bearer для
// REST-подписки; SocketToken приходит из Client.SocketToken.
type CentrifugoOptions struct {
	WSURL       string // пусто → DefaultCentrifugoURL
	SocketToken string
	Channel     string // $alerts:donation_<user_id>
	Client      *Client
	OnDonation  func(DonationEvent) // вызывается из горутины чтения Run
}

// Centrifugo — клиент WS DA: connect по socket_connection_token → REST-подписка
// канала с client id → client-side подписка токеном канала → публикации.
type Centrifugo struct {
	opts CentrifugoOptions
}

// NewCentrifugo готовит клиента, подставляя дефолтный WS-эндпоинт.
func NewCentrifugo(opts CentrifugoOptions) *Centrifugo {
	if opts.WSURL == "" {
		opts.WSURL = DefaultCentrifugoURL
	}
	return &Centrifugo{opts: opts}
}

// Run — до ctx.Done: соединение → REST-подписка (client id из connect) →
// WS-подписка токеном канала → цикл чтения публикаций; при отмене ctx
// возвращает nil.
func (c *Centrifugo) Run(ctx context.Context) error {
	dialer := websocket.Dialer{}
	conn, _, err := dialer.DialContext(ctx, c.opts.WSURL, nil)
	if err != nil {
		return fmt.Errorf("donationalerts: centrifugo dial: %w", err)
	}
	defer conn.Close()

	var clientID string
	if err := c.requestResponse(conn, 0, map[string]any{
		"token":   c.opts.SocketToken,
		"name":    "timer-widget",
		"version": "1.0",
	}, &clientID); err != nil {
		return fmt.Errorf("donationalerts: centrifugo connect: %w", err)
	}
	token, err := c.opts.Client.SubscribeCentrifuge(ctx, c.opts.Channel, clientID)
	if err != nil {
		return fmt.Errorf("donationalerts: centrifugo REST-подписка: %w", err)
	}
	var ok struct{}
	if err := c.requestResponse(conn, 1, map[string]any{
		"channel": c.opts.Channel,
		"token":   token,
	}, &ok); err != nil {
		return fmt.Errorf("donationalerts: centrifugo subscribe: %w", err)
	}

	// Отмена ctx прерывает блокирующее чтение: сторож ставит read-deadline.
	go func() {
		<-ctx.Done()
		_ = conn.SetReadDeadline(time.Now())
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil // штатная отмена
			}
			return fmt.Errorf("donationalerts: centrifugo чтение: %w", err)
		}
		var msg struct {
			Method int             `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.Method != 1 {
			continue // пинги и прочие служебные пуши
		}
		var pub struct {
			Channel string `json:"channel"`
			Data    struct {
				Type string        `json:"type"`
				Data DonationEvent `json:"data"`
			} `json:"data"`
		}
		if json.Unmarshal(msg.Params, &pub) != nil {
			continue
		}
		if pub.Channel != c.opts.Channel || pub.Data.Type != "donation" {
			continue
		}
		if c.opts.OnDonation != nil {
			c.opts.OnDonation(pub.Data.Data)
		}
	}
}

// requestResponse — отправляет клиентское сообщение (method+id) и ждёт ответ
// с тем же id, пропуская серверные пуши; для connect вытаскивает client id
// из result, для subscribe подтверждает успех (result есть — ошибки нет).
func (c *Centrifugo) requestResponse(conn *websocket.Conn, method int, params map[string]any, out any) error {
	id := method + 1 // connect=1, subscribe=2
	req, err := json.Marshal(map[string]any{"method": method, "id": id, "params": params})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, req); err != nil {
		return fmt.Errorf("запись: %w", err)
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.ID != id {
			continue // пуш или чужой ответ
		}
		if msg.Error != nil {
			return fmt.Errorf("сервер: код %d: %s", msg.Error.Code, msg.Error.Message)
		}
		if method == 0 {
			var res struct {
				Client string `json:"client"`
			}
			if err := json.Unmarshal(msg.Result, &res); err != nil {
				return fmt.Errorf("result: %w", err)
			}
			if res.Client == "" {
				return errors.New("результат без client id")
			}
			if s, is := out.(*string); is && s != nil {
				*s = res.Client
				return nil
			}
			return errors.New("out должен быть *string")
		}
		return json.Unmarshal(msg.Result, out)
	}
}
