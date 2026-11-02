package twitch

import (
	"context"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/rules"
	"github.com/gorilla/websocket"
)

func testSubRule() rules.SubRule {
	return rules.SubRule{Tier1000: 60, Tier2000: 120, Tier3000: 300}
}

// TestSubSeconds — e04s02-t3: секунды за саб по тиру; ре-саб считается по
// правилу своего тира.
func TestSubSeconds(t *testing.T) {
	r := testSubRule()
	cases := []struct {
		tier string
		want time.Duration
	}{
		{"1000", 60 * time.Second},
		{"2000", 120 * time.Second},
		{"3000", 300 * time.Second},
	}
	for _, c := range cases {
		got, err := SubSeconds(r, c.tier)
		if err != nil {
			t.Errorf("tier %s: %v", c.tier, err)
			continue
		}
		if got != c.want {
			t.Errorf("tier %s: %v, want %v", c.tier, got, c.want)
		}
	}
	if _, err := SubSeconds(r, "9999"); err == nil {
		t.Error("unknown tier: want ошибка")
	}
}

// TestWSNotificationDispatch — e04s02-t3: notification-кадры EventSub
// разворачиваются в колбэки: channel.subscribe (тир),
// channel.subscription.message (месяцы), ревок.
func TestWSNotificationDispatch(t *testing.T) {
	subCh := make(chan SubEvent, 1)
	resubCh := make(chan ResubEvent, 1)
	revokeCh := make(chan struct{}, 1)

	s := newWSServer(t, func(t *testing.T, ws *websocket.Conn, dial int) {
		sendEvent(t, ws, "session_welcome", welcomePayload("sess-1", 30, ""))
		sendEvent(t, ws, "notification", map[string]any{
			"subscription": map[string]any{"type": "channel.subscribe", "version": "1", "status": "enabled"},
			"event":        map[string]any{"broadcaster_user_id": "123", "tier": "1000", "is_gift": true},
		})
		sendEvent(t, ws, "notification", map[string]any{
			"subscription": map[string]any{"type": "channel.subscription.message", "version": "1", "status": "enabled"},
			"event":        map[string]any{"broadcaster_user_id": "123", "tier": "2000", "cumulative_months": 7},
		})
		sendEvent(t, ws, "notification", map[string]any{
			"subscription": map[string]any{"type": "channel.subscribe", "status": "revoked"},
			"event":        nil,
		})
		_ = ws.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, _ = ws.ReadMessage() // до закрытия клиентом
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- NewEventSub(EventSubOptions{
			WSURL: s.url,
			OnSub: func(ev SubEvent) {
				select {
				case subCh <- ev:
				default:
				}
			},
			OnResub: func(ev ResubEvent) {
				select {
				case resubCh <- ev:
				default:
				}
			},
			OnRevoke: func() {
				select {
				case revokeCh <- struct{}{}:
				default:
				}
			},
		}).Run(ctx)
	}()

	select {
	case ev := <-subCh:
		if ev.Tier != "1000" {
			t.Errorf("SubEvent = %+v, want tier 1000", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnSub не вызван для channel.subscribe")
	}
	select {
	case ev := <-resubCh:
		if ev.Tier != "2000" || ev.CumulativeMonths != 7 {
			t.Errorf("ResubEvent = %+v, want tier 2000, 7 месяцев", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnResub не вызван для channel.subscription.message")
	}
	select {
	case <-revokeCh:
	case <-time.After(2 * time.Second):
		t.Fatal("OnRevoke не вызван для revoked")
	}
	stopEventSub(t, cancel, done)
}
