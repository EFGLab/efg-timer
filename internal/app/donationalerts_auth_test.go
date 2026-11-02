package app

import (
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
)

// Тесты неблокирующего входа DA.
//
// Проверяем то, что легко сломать незаметно: перенос полей входа не
// должен затирать обычные настройки донатов (время и порог суммы) —
// они могут быть несохранёнными правками диалога.

func TestCopyDonationAlertsAuthFieldsPreservesDonationSettings(t *testing.T) {
	draft := settings.DonationAlertsConfig{
		Time:      settings.TimeSettings{Value: 7, Unit: settings.TimeUnitHour},
		PerAmount: 42,
	}
	expiresAt := time.Now().Add(time.Hour)
	persisted := settings.DonationAlertsConfig{
		Enabled:   true,
		UserID:    1234,
		ExpiresAt: &expiresAt,
		Time: settings.TimeSettings{
			Value: 1,
			Unit:  settings.TimeUnitMinute,
		},
		PerAmount: 100,
	}

	copyDonationAlertsAuthFields(&draft, persisted)

	if !draft.Enabled || draft.UserID != 1234 {
		t.Fatalf("auth fields = %+v", draft)
	}
	if draft.ExpiresAt == nil || !draft.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %v", draft.ExpiresAt)
	}
	if draft.Time.Value != 7 || draft.Time.Unit != settings.TimeUnitHour || draft.PerAmount != 42 {
		t.Fatalf("несохранённые поля перезаписаны: %+v", draft)
	}
}

// Копирование не должно делить указатель с источником: правка в одном
// месте не должна менять другое.
func TestCopyDonationAlertsAuthFieldsCopiesExpiresAt(t *testing.T) {
	expiresAt := time.Now().Add(time.Hour)
	src := settings.DonationAlertsConfig{Enabled: true, UserID: 7, ExpiresAt: &expiresAt}

	var dst settings.DonationAlertsConfig
	copyDonationAlertsAuthFields(&dst, src)

	if dst.ExpiresAt == nil {
		t.Fatal("expires_at не скопирован")
	}
	if dst.ExpiresAt == src.ExpiresAt {
		t.Fatal("expires_at скопирован указателем, а не значением")
	}
	if !dst.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %v, ждали %v", dst.ExpiresAt, expiresAt)
	}
}

func TestSetDonationAlertsConnectedAndClearKeepDonationSettings(t *testing.T) {
	cfg := settings.DonationAlertsConfig{
		Time:      settings.TimeSettings{Value: 9, Unit: settings.TimeUnitMinute},
		PerAmount: 250,
	}
	session := providers.DonationAlertsSession{
		UserID:    555,
		ExpiresAt: time.Now().Add(time.Hour),
	}

	setDonationAlertsConnected(&cfg, session)
	if !cfg.Enabled || cfg.UserID != 555 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.ExpiresAt == nil {
		t.Fatal("expires_at не задан")
	}

	clearDonationAlertsSettings(&cfg)
	if cfg.Enabled || cfg.UserID != 0 || cfg.ExpiresAt != nil {
		t.Fatalf("cfg после clear = %+v", cfg)
	}
	if cfg.Time.Value != 9 || cfg.PerAmount != 250 {
		t.Fatalf("правила донатов очищены: %+v", cfg)
	}
}

// TestDonationAlertsEnableDoesNotBlockFrame — главное свойство входа DA:
// кнопка Enable не блокирует кадр.
//
// Провайдер без client_secret: вход невозможен, и это не мешает проверить
// главное — обработчик возвращается сразу, вход идёт в горутине, а ошибка
// уходит в диалог (а не теряется в консоли).
//
// Обработчик зовём напрямую (собранный gui.EventCtx): в тестовом окне без
// SVG-парсера сайдбар растягивает раздел DA за пределы окна, и клик по
// кнопке не попадает в её видимую область.
func TestDonationAlertsEnableDoesNotBlockFrame(t *testing.T) {
	st := state.InitialAppState()
	st.Timer = timer.New(time.Minute)
	st.Providers = []providers.Provider{
		providers.NewDonationAlertsProvider(providers.DonationAlertsOptions{ClientID: "21606"}),
	}
	draft, err := st.Settings.Copy()
	if err != nil {
		t.Fatalf("копия настроек: %v", err)
	}
	st.SettingsState.Settings = draft
	st.SettingsState.CurrentSection = state.SettingSectionDonationAlerts

	w := gui.NewTestWindow(gateCleanuper{t: t}, gui.WindowCfg{
		AppInfo: info,
		State:   st,
		Width:   800,
		Height:  600,
	})
	w.TestRender(settingsDialogContent)

	ev := &gui.Event{Type: gui.EventMouseDown}
	ctx := gui.EventCtx{Window: w, Event: ev}

	done := make(chan struct{})
	go func() {
		viewDialogSettingsDonationAlertsEnableHandler(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("обработчик Enable заблокировал кадр")
	}

	// Событие помечено обработанным (правило go-gui: обработчик, который
	// действует по событию, зовёт ctx.Consume()).
	if !ev.IsHandled {
		t.Fatal("обработчик Enable не пометил событие обработанным")
	}

	// Обработчик отвечает сразу: в диалоге либо «вход идёт», либо уже
	// пришла ошибка (ошибка приходит через очередь окна — в тестовом окне
	// очередь разбирает кадр, которого тут нет, поэтому обычно висит
	// «вход идёт»).
	auth := gui.State[state.AppState](w).SettingsState.DonationAlertsAuth
	if !auth.Pending && auth.Error == "" {
		t.Fatalf("вход DA не начался: %+v", auth)
	}
}
