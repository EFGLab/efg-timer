package providers

import (
	"context"
	"errors"
	"fmt"
	"time"

	twitchapi "github.com/EFGLab/efg-timer/internal/integrations/twitch"
)

// TwitchSubscriptionEvent — событие подписки Twitch (e11s02-t1).
// Саб и ре-саб приходят одним типом (EventTypeSubscription): ре-саб
// отличается флагом Resub и счётчиком CumulativeMonths. Подаренный саб от
// купленного не отличаем.
type TwitchSubscriptionEvent struct {
	Tier             string `json:"tier"`
	CumulativeMonths int64  `json:"cumulative_months,omitempty"`
	Resub            bool   `json:"resub,omitempty"`
}

// StartEvents запускает поток событий Twitch (EventSub WebSocket) и
// блокируется до отмены ctx. При обрыве соединения legacy-клиент
// переподключается сам (с паузой) и переподписывается после welcome —
// поэтому отдельный backoff здесь не нужен (e11s02-t1).
//
// Возвращает nil, если поток остановлен отменой ctx; ошибка возвращается
// только если события начать нельзя (нет сессии, не обновился токен).
func (p *TwitchProvider) StartEvents(ctx context.Context, broadcasterID string) error {
	if broadcasterID == "" {
		return errors.New("twitch: для событий нужен broadcaster_id")
	}

	eventsCtx, cancel := context.WithCancel(ctx)

	// Регистрируем поток: предыдущий (если был) останавливаем — у
	// провайдера один поток событий. eventsGen защищает от того, что
	// завершившийся поток сбросит состояние уже нового (e11s02-t1).
	p.mu.Lock()
	prev := p.eventsCancel
	p.eventsGen++
	gen := p.eventsGen
	p.eventsCancel = cancel
	p.eventsRunning = true
	p.mu.Unlock()
	if prev != nil {
		prev()
	}

	defer func() {
		cancel()
		p.mu.Lock()
		if p.eventsGen == gen {
			p.eventsRunning = false
			p.eventsCancel = nil
		}
		p.mu.Unlock()
	}()

	session, err := p.eventsSession(ctx)
	if err != nil {
		return err
	}

	// Legacy-клиент сам обновляет токен при 401, но протухший access-токен
	// лучше обновить заранее: подписка EventSub уходит сразу после welcome,
	// и лишний 401-цикл обрывает соединение (e11s02-t1).
	client := twitchapi.NewClient(twitchapi.ClientOptions{
		ClientID:         p.clientID,
		Session:          toLegacySession(session),
		TokenURL:         p.tokenURL,
		UsersURL:         p.usersURL,
		SubscriptionsURL: p.eventSubSubscribeURL,
		HTTPClient:       p.http,
		OnRefresh:        p.eventsOnRefresh(ctx, session),
	})

	if !session.ExpiresAt.IsZero() && !time.Now().Before(session.ExpiresAt) {
		if _, err := client.Refresh(ctx); err != nil {
			return fmt.Errorf("twitch: обновление токена для событий: %w", err)
		}
	}

	es := twitchapi.NewEventSub(twitchapi.EventSubOptions{
		WSURL:         p.eventSubURL,
		Client:        client,
		BroadcasterID: broadcasterID,
		OnSub: func(ev twitchapi.SubEvent) {
			p.emit(EventTypeSubscription, TwitchSubscriptionEvent{
				Tier: ev.Tier,
			})
		},
		OnResub: func(ev twitchapi.ResubEvent) {
			p.emit(EventTypeSubscription, TwitchSubscriptionEvent{
				Tier:             ev.Tier,
				CumulativeMonths: ev.CumulativeMonths,
				Resub:            true,
			})
		},
	})

	// Run сам переподключается при обрыве и возвращает nil при отмене ctx.
	return es.Run(eventsCtx)
}

// StopEvents останавливает поток событий: отменяет его контекст (e11s02-t1).
// Поток завершается сам, StartEvents возвращает nil.
func (p *TwitchProvider) StopEvents() {
	p.mu.Lock()
	cancel := p.eventsCancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// EventsRunning — идёт ли поток событий (e11s02-t1). IsRunning про вход.
func (p *TwitchProvider) EventsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.eventsRunning
}

// eventsSession возвращает сессию для потока событий: если в памяти её
// нет — читаем из OS credential store. found=false — входа не было.
func (p *TwitchProvider) eventsSession(ctx context.Context) (TwitchSession, error) {
	session, ok := p.Session()
	if ok {
		return session, nil
	}
	loaded, found, err := p.LoadSession(ctx)
	if err != nil {
		return TwitchSession{}, err
	}
	if !found {
		return TwitchSession{}, errors.New("twitch: нет сессии — войдите в Twitch")
	}
	return loaded, nil
}

// eventsOnRefresh сохраняет обновлённую legacy-сессию: Twitch ротирует
// refresh-токен, поэтому старый после обновления может не работать —
// сохранять надо сразу (e11s02-t1).
func (p *TwitchProvider) eventsOnRefresh(ctx context.Context, base TwitchSession) func(twitchapi.TwitchSession) {
	return func(s twitchapi.TwitchSession) {
		updated := fromLegacySession(s, base)
		updated.ExpiresAt = twitchExpiresAt(s.ExpiresIn)
		updated.Scopes = base.Scopes
		if updated.RefreshToken == "" {
			// Refresh-ответ без нового refresh_token — оставляем старый.
			updated.RefreshToken = base.RefreshToken
		}
		p.setSession(updated)
		// Ошибка сохранения не должна рвать поток событий: сессия уже
		// обновлена в памяти, следующая попытка её запишет.
		_ = p.SaveSession(ctx, updated)
	}
}
