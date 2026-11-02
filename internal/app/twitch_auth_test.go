package app

import (
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/settings"
)

func TestCopyTwitchAuthFieldsPreservesTierSettings(t *testing.T) {
	draft := settings.TwitchConfig{
		Tier1SubTime:  settings.TimeSettings{Value: 7, Unit: settings.TimeUnitHour},
		PerBitsAmount: 42,
	}
	persisted := settings.TwitchConfig{
		Enabled: true,
		Login:   "streamer",
		UserID:  "42",
		Tier1SubTime: settings.TimeSettings{
			Value: 1,
			Unit:  settings.TimeUnitMinute,
		},
	}

	copyTwitchAuthFields(&draft, persisted)

	if !draft.Enabled || draft.Login != "streamer" || draft.UserID != "42" {
		t.Fatalf("auth fields = %+v", draft)
	}
	if draft.Tier1SubTime.Value != 7 || draft.Tier1SubTime.Unit != settings.TimeUnitHour || draft.PerBitsAmount != 42 {
		t.Fatalf("несохранённые поля перезаписаны: %+v", draft)
	}
}

func TestSetTwitchConnectedAndClearKeepTierSettings(t *testing.T) {
	cfg := settings.TwitchConfig{
		Tier1SubTime: settings.TimeSettings{Value: 9, Unit: settings.TimeUnitMinute},
	}
	session := providers.TwitchSession{ExpiresAt: time.Now().Add(time.Hour)}
	setTwitchConnected(&cfg, providers.TwitchUser{ID: "42", Login: "streamer"}, session)
	if !cfg.Enabled || cfg.Login != "streamer" || cfg.UserID != "42" {
		t.Fatalf("cfg = %+v", cfg)
	}

	clearTwitchSettings(&cfg)
	if cfg.Enabled || cfg.Login != "" || cfg.UserID != "" || cfg.ExpiresAt != nil {
		t.Fatalf("cfg после clear = %+v", cfg)
	}
	if cfg.Tier1SubTime.Value != 9 {
		t.Fatalf("тир очищен: %+v", cfg)
	}
}
