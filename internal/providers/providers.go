// Package providers — общий интерфейс провайдеров событий (Twitch,
// DonationAlerts) и их реализации.
package providers

import "context"

type ProviderName string
type EventType string

const (
	EventTypeSubscription EventType = "subscription"
	EventTypeDonation     EventType = "donation"
)

type EventHandler func(eventType EventType, eventData interface{})

type Provider interface {
	On(handler EventHandler)
	IsRunning() bool
	Enable(ctx context.Context) (string, error)
	Disable()
	GetName() ProviderName
}

type Providers []Provider

func (p Providers) GetProvider(providerName ProviderName) Provider {
	for _, provider := range p {
		if provider.GetName() == providerName {
			return provider
		}
	}

	return nil
}
