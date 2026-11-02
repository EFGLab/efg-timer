package donationalerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

const DefaultCentrifugoURL = "wss://centrifugo.donationalerts.com/connection/websocket"

type DonationEvent struct {
	ID       int64   `json:"id"`
	Username string  `json:"username"`
	Message  string  `json:"message"`
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type CentrifugoOptions struct {
	WSURL       string
	SocketToken string
	Channel     string
	Client      *Client
	OnDonation  func(DonationEvent)
}

type Centrifugo struct {
	opts CentrifugoOptions
}

func NewCentrifugo(opts CentrifugoOptions) *Centrifugo {
	if opts.WSURL == "" {
		opts.WSURL = DefaultCentrifugoURL
	}
	return &Centrifugo{opts: opts}
}

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

	go func() {
		<-ctx.Done()
		_ = conn.SetReadDeadline(time.Now())
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("donationalerts: centrifugo чтение: %w", err)
		}
		var msg struct {
			Method int             `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.Method != 1 {
			continue
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

func (c *Centrifugo) requestResponse(conn *websocket.Conn, method int, params map[string]any, out any) error {
	id := method + 1
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
			continue
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
