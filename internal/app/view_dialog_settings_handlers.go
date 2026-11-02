package app

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/EFGLab/efg-timer/internal/currency"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
)

func viewDialogSettingsTwitchTier1SubValueHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.Twitch.Tier1SubTime.Value = int64(number)
	ctx.Consume()
}

func viewDialogSettingsTwitchTier1SubUnitHandler(sel []string, ctx gui.EventCtx) {
	if len(sel) != 1 {
		return
	}
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.Settings.Providers.Twitch.Tier1SubTime.Unit = settings.TimeUnit(sel[0])
	ctx.Consume()
}

func viewDialogSettingsTwitchTier2SubValueHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.Twitch.Tier2SubTime.Value = int64(number)
	ctx.Consume()
}

func viewDialogSettingsTwitchTier2SubUnitHandler(sel []string, ctx gui.EventCtx) {
	if len(sel) != 1 {
		return
	}
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.Settings.Providers.Twitch.Tier2SubTime.Unit = settings.TimeUnit(sel[0])
	ctx.Consume()
}

func viewDialogSettingsTwitchTier3SubValueHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.Twitch.Tier3SubTime.Value = int64(number)
	ctx.Consume()
}

func viewDialogSettingsTwitchTier3SubUnitHandler(sel []string, ctx gui.EventCtx) {
	if len(sel) != 1 {
		return
	}
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.Settings.Providers.Twitch.Tier3SubTime.Unit = settings.TimeUnit(sel[0])
	ctx.Consume()
}

func viewDialogSettingsTwitchCountResubHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	twitchCfg := &st.SettingsState.Settings.Providers.Twitch
	twitchCfg.CountResub = !twitchCfg.CountResub
	ctx.Consume()
}

func viewDialogSettingsTwitchBitsValueHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.Twitch.BitsTime.Value = int64(number)
	ctx.Consume()
}

func viewDialogSettingsTwitchBitsUnitHandler(sel []string, ctx gui.EventCtx) {
	if len(sel) != 1 {
		return
	}
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.Settings.Providers.Twitch.BitsTime.Unit = settings.TimeUnit(sel[0])
	ctx.Consume()
}

func viewDialogSettingsTwitchBitsHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.Twitch.PerBitsAmount = int64(number)
	ctx.Consume()
}

func viewDialogSettingsDonationAlertsValueHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.DonationAlerts.Time.Value = int64(number)
	ctx.Consume()
}

func viewDialogSettingsDonationAlertsUnitHandler(sel []string, ctx gui.EventCtx) {
	if len(sel) != 1 {
		return
	}
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.Settings.Providers.DonationAlerts.Time.Unit = settings.TimeUnit(sel[0])
	ctx.Consume()
}

func viewDialogSettingsDonationAlertsMoneyHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.DonationAlerts.PerAmount = int64(number)
	ctx.Consume()
}

func viewDialogSettingsDonateXValueHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.DonateX.Time.Value = int64(number)
	ctx.Consume()
}

func viewDialogSettingsDonateXUnitHandler(sel []string, ctx gui.EventCtx) {
	if len(sel) != 1 {
		return
	}
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.Settings.Providers.DonateX.Time.Unit = settings.TimeUnit(sel[0])
	ctx.Consume()
}

func viewDialogSettingsDonateXMoneyHandler(s string, ctx gui.EventCtx) {
	number, err := strconv.Atoi(s)
	if err != nil {
		return
	}
	gui.State[state.AppState](ctx.Window).SettingsState.Settings.Providers.DonateX.PerAmount = int64(number)
	ctx.Consume()
}

func settingsDialogViewTwitchSectionHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.CurrentSection = state.SettingSectionTwitch
	ctx.Consume()
}

func settingsDialogViewDonationAlertsSectionHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.CurrentSection = state.SettingSectionDonationAlerts
	ctx.Consume()
}

func settingsDialogViewDonateXSectionHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.CurrentSection = state.SettingSectionDonateX
	ctx.Consume()
}

func settingsDialogViewCurrenciesSectionHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	st.SettingsState.CurrentSection = state.SettingSectionCurrencies
	ctx.Consume()
}

func viewDialogSettingsDonateXEnableHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)

	st.SettingsState.Settings.Providers.DonateX.Enabled = true

	ctx.Consume()
}

func viewDialogSettingsDonateXDisableHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)

	st.SettingsState.Settings.Providers.DonateX.Enabled = false

	ctx.Consume()
}

func viewDialogSettingsCurrenciesBaseHandler(sel []string, ctx gui.EventCtx) {
	if len(sel) != 1 {
		return
	}
	base := settings.Currency(sel[0])
	if !knownCurrency(base) {
		return
	}

	st := gui.State[state.AppState](ctx.Window)
	draft := &st.SettingsState.Settings

	if !draft.ReanchorCurrencyRates(base) {
		draft.Currencies = map[settings.Currency]float64{base: 1}
		st.SettingsState.Currencies.UpdatedAt = time.Time{}
	}
	draft.BaseCurrency = base

	st.SettingsState.Currencies.NeedsRefresh = !allRatesPresent(draft.Currencies)
	st.SettingsState.Currencies.Error = ""

	ctx.Window.InvalidateLayout()
	ctx.Consume()
}

func viewDialogSettingsCurrenciesRateHandler(code settings.Currency) func(string, gui.EventCtx) {
	return func(s string, ctx gui.EventCtx) {
		rate, err := parseCurrencyRate(s)
		if err != nil {
			fmt.Println("currency: неверный курс", string(code), s, err)
			return
		}

		st := gui.State[state.AppState](ctx.Window)
		draft := &st.SettingsState.Settings
		if draft.Currencies == nil {
			draft.Currencies = map[settings.Currency]float64{}
		}
		draft.Currencies[code] = rate

		st.SettingsState.Currencies.UpdatedAt = time.Time{}
		st.SettingsState.Currencies.Error = ""
		st.SettingsState.Currencies.NeedsRefresh = !allRatesPresent(draft.Currencies)

		ctx.Consume()
	}
}

func viewDialogSettingsCurrenciesRefreshHandler(ctx gui.EventCtx) {
	st := gui.State[state.AppState](ctx.Window)
	if st.SettingsState.Currencies.Pending {
		return
	}

	base := baseCurrency(st.SettingsState.Settings.BaseCurrency)
	st.SettingsState.Currencies.Pending = true
	st.SettingsState.Currencies.Error = ""
	ctx.Window.InvalidateLayout()

	go fetchCurrenciesRates(ctx.Window, string(base))
	ctx.Consume()
}

var fetchRates = currency.FetchRates

func fetchCurrenciesRates(w *gui.Window, base string) {
	ctx, cancel := context.WithTimeout(w.Ctx(), 20*time.Second)
	defer cancel()

	rates, err := fetchRates(ctx, base)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		fmt.Println("currency: fetch rates:", err)
		w.QueueCommand(func(w *gui.Window) {
			st := gui.State[state.AppState](w)
			st.SettingsState.Currencies.Pending = false
			st.SettingsState.Currencies.Error =
				"не удалось обновить курсы: " + err.Error()
			w.InvalidateLayout()
		})
		return
	}
	if ctx.Err() != nil {
		return
	}

	w.QueueCommand(func(w *gui.Window) {
		st := gui.State[state.AppState](w)
		st.SettingsState.Currencies.Pending = false

		if baseCurrency(st.SettingsState.Settings.BaseCurrency) != settings.Currency(base) {
			st.SettingsState.Currencies.NeedsRefresh = true
			w.InvalidateLayout()
			return
		}

		draft := &st.SettingsState.Settings
		if draft.Currencies == nil {
			draft.Currencies = map[settings.Currency]float64{}
		}
		for code, rate := range rates.Rates {
			cur := settings.Currency(code)
			if !knownCurrency(cur) || rate <= 0 {
				continue
			}
			draft.Currencies[cur] = rate
		}
		draft.Currencies[settings.Currency(base)] = 1

		st.SettingsState.Currencies.Error = ""
		st.SettingsState.Currencies.UpdatedAt = rates.UpdatedAt
		if st.SettingsState.Currencies.UpdatedAt.IsZero() {
			st.SettingsState.Currencies.UpdatedAt = time.Now()
		}
		st.SettingsState.Currencies.NeedsRefresh = !allRatesPresent(draft.Currencies)
		w.InvalidateLayout()
	})
}

func viewDialogSettingsSaveSettingsHandler(ctx gui.EventCtx) {
	ctx.Consume()

	st := gui.State[state.AppState](ctx.Window)
	err := gui.SaveSettings(ctx.Window, st.SettingsState.Settings)
	if err != nil {
		fmt.Println(err)
		return
	}

	st.Settings, err = st.SettingsState.Settings.Copy()
	if err != nil {
		fmt.Println(err)
		return
	}

	applyRulesToStore(st)

	ctx.Window.DialogDismiss()
}
