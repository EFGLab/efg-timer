package twitch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
)

// DefaultEventSubURL — EventSub WebSocket (e04s02-t1).
const DefaultEventSubURL = "wss://eventsub.wss.twitch.tv/ws"

// keepaliveGrace — запас сверх keepalive_timeout_seconds: нет сообщений
// дольше таймаута + запаса → соединение считается потерянным, реконнект.
const keepaliveGrace = time.Second

// EventSubOptions — параметры EventSub-клиента. WSURL переопределяется в
// тестах; OnSession вызывается после каждого welcome (после reconnect_url и
// обычного реконнекта тоже).
type EventSubOptions struct {
	WSURL         string
	Client        *Client // Helix-клиент для подписок (nil — только жизненный цикл)
	BroadcasterID string  // канал для condition; пусто — без подписок
	OnSession     func(sessionID string, keepalive time.Duration, reconnectURL string)
	OnSub         func(SubEvent)
	OnResub       func(ResubEvent)
	OnRevoke      func()
}

// frame — общий конверт EventSub-протокола.
type frame struct {
	Metadata struct {
		MessageType string `json:"message_type"`
		MessageID   string `json:"message_id"`
		Timestamp   string `json:"message_timestamp"`
	} `json:"metadata"`
	Payload json.RawMessage `json:"payload"`
}

// sessionPayload — payload welcome и session_reconnect.
type sessionPayload struct {
	Session struct {
		ID                      string `json:"id"`
		Status                  string `json:"status"`
		KeepaliveTimeoutSeconds int    `json:"keepalive_timeout_seconds"`
		ReconnectURL            string `json:"reconnect_url"`
	} `json:"session"`
}

// notificationPayload — payload уведомлений EventSub (e04s02-t3).
type notificationPayload struct {
	Subscription struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	} `json:"subscription"`
	Event struct {
		Tier             string `json:"tier"`
		CumulativeMonths int64  `json:"cumulative_months"`
	} `json:"event"`
}

// EventSub — клиент EventSub WebSocket: жизненный цикл соединения
// (welcome, ping/pong, keepalive-таймаут, session_reconnect). Уведомления
// обрабатываются через колбэки (e04s02-t3); подписка — Helix-REST (t2).
type EventSub struct {
	wsURL         string
	client        *Client
	broadcasterID string
	onSession     func(sessionID string, keepalive time.Duration, reconnectURL string)
	onSub         func(SubEvent)
	onResub       func(ResubEvent)
	onRevoke      func()
}

// NewEventSub создаёт клиент; недостающие параметры добираются дефолтом.
func NewEventSub(opts EventSubOptions) *EventSub {
	c := &EventSub{
		wsURL:         opts.WSURL,
		client:        opts.Client,
		broadcasterID: opts.BroadcasterID,
		onSession:     opts.OnSession,
		onSub:         opts.OnSub,
		onResub:       opts.OnResub,
		onRevoke:      opts.OnRevoke,
	}
	if c.wsURL == "" {
		c.wsURL = DefaultEventSubURL
	}
	return c
}

// Run управляет соединением до отмены ctx: разрыв (keepalive-таймаут,
// session_reconnect, сбой сети) — повторное подключение. При отмене
// возвращает nil; ошибки отдельных попыток не фатальны.
func (c *EventSub) Run(ctx context.Context) error {
	nextURL := ""
	for {
		url, pause := c.wsURL, time.Second
		if nextURL != "" {
			url, pause, nextURL = nextURL, 0, ""
		}
		if pause > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(pause):
			}
		}
		nextURL, _ = c.serve(ctx, url)
		if ctx.Err() != nil {
			return nil
		}
	}
}

// serve — одна жизнь соединения: dial → welcome → цикл чтения.
// Возвращает reconnect_url, если сервер запросил переход.
func (c *EventSub) serve(ctx context.Context, url string) (string, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.DialContext(ctx, url, nil)
	if err != nil {
		return "", fmt.Errorf("eventsub dial: %w", err)
	}
	defer conn.Close()

	// сторож: отмена ctx прерывает блокирующее чтение. Только Close
	// потокобезопасен с активным ReadMessage (SetReadDeadline из другой
	// горутины gorilla не гарантирует) — иначе висяк при отмене.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()

	// welcome — первое сообщение соединения
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	f, err := readFrame(conn)
	if err != nil {
		return "", fmt.Errorf("eventsub welcome: %w", err)
	}
	if f.Metadata.MessageType != "session_welcome" {
		return "", fmt.Errorf("eventsub: welcome не получен (%s)", f.Metadata.MessageType)
	}
	var sp sessionPayload
	if err := json.Unmarshal(f.Payload, &sp); err != nil {
		return "", fmt.Errorf("eventsub welcome payload: %w", err)
	}
	keepalive := time.Duration(sp.Session.KeepaliveTimeoutSeconds) * time.Second
	if c.onSession != nil {
		c.onSession(sp.Session.ID, keepalive, sp.Session.ReconnectURL)
	}
	// подписки — после каждого welcome (в т.ч. после реконнекта), e04s02-t2;
	// ошибка → реконнект, следующий welcome переподпишет
	if c.client != nil && c.broadcasterID != "" {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = c.client.SubscribeEventSub(sctx, sp.Session.ID, c.broadcasterID)
		cancel()
		if err != nil {
			return "", fmt.Errorf("eventsub subscribe: %w", err)
		}
	}

	// цикл чтения: после каждого сообщения продлеваем deadline
	extend := func() {
		if keepalive > 0 {
			_ = conn.SetReadDeadline(time.Now().Add(keepalive + keepaliveGrace))
		}
	}
	extend()
	for {
		f, err := readFrame(conn)
		if err != nil {
			return "", err // keepalive-таймаут или разрыв → реконнект
		}
		extend()
		switch f.Metadata.MessageType {
		case "session_ping":
			pong := frame{}
			pong.Metadata.MessageType = "session_pong"
			pong.Metadata.MessageID = fmt.Sprintf("pong-%d", time.Now().UnixNano())
			pong.Metadata.Timestamp = time.Now().UTC().Format(time.RFC3339)
			if err := conn.WriteJSON(pong); err != nil {
				return "", fmt.Errorf("eventsub pong: %w", err)
			}
		case "session_reconnect":
			var rp sessionPayload
			if err := json.Unmarshal(f.Payload, &rp); err != nil {
				return "", fmt.Errorf("eventsub reconnect payload: %w", err)
			}
			return rp.Session.ReconnectURL, nil
		case "notification":
			var np notificationPayload
			if err := json.Unmarshal(f.Payload, &np); err != nil {
				continue // битый payload — не роняем соединение
			}
			if np.Subscription.Status == "revoked" {
				if c.onRevoke != nil {
					c.onRevoke()
				}
				continue
			}
			switch np.Subscription.Type {
			case "channel.subscribe":
				if c.onSub != nil {
					c.onSub(SubEvent{Tier: np.Event.Tier})
				}
			case "channel.subscription.message":
				if c.onResub != nil {
					c.onResub(ResubEvent{Tier: np.Event.Tier, CumulativeMonths: np.Event.CumulativeMonths})
				}
			}
		default:
		}
	}
}

func readFrame(conn *websocket.Conn) (frame, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return frame{}, err
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		return frame{}, fmt.Errorf("eventsub кадр: %w", err)
	}
	return f, nil
}
