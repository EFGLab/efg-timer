package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
)

// currencyCodes — валюты раздела «Валюты» в фиксированном порядке.
var currencyCodes = []settings.Currency{
	settings.CurrencyUSD,
	settings.CurrencyEUR,
	settings.CurrencyRUB,
}

// currencyOptions — варианты выбора базовой валюты.
var currencyOptions = []gui.SelectOption{
	{Label: string(settings.CurrencyUSD), Value: string(settings.CurrencyUSD)},
	{Label: string(settings.CurrencyEUR), Value: string(settings.CurrencyEUR)},
	{Label: string(settings.CurrencyRUB), Value: string(settings.CurrencyRUB)},
}

// Раздел «Валюты» в диалоге настроек: базовая валюта, курсы к ней и
// кнопка обновления курсов из внешнего API.
//
// Курс валюты — сколько единиц базовой валюты в одной единице этой
// валюты, поэтому строка базовой валюты всегда «1 <код> = 1 <код>» и
// её поле не редактируется.
func settingsDialogCurrenciesSection(w *gui.Window) []gui.View {
	gState := gui.State[state.AppState](w)
	theme := gui.CurrentTheme()

	base := baseCurrency(gState.SettingsState.Settings.BaseCurrency)

	rows := make([]gui.View, 0, len(currencyCodes))
	for _, code := range currencyCodes {
		rows = append(rows, currencyRateRow(gState, base, code))
	}

	return []gui.View{
		gui.Column(gui.ContainerCfg{
			Sizing:  gui.FillFill,
			Padding: gui.PadVH(theme.PaddingMedium.Top, 0),
			Content: []gui.View{
				gui.Text(gui.TextCfg{
					Sizing:    gui.FillFit,
					Text:      "Валюты",
					TextStyle: theme.TextStyleTitle,
				}),
				gui.Label("Базовая валюта", theme.TextStyleTitleSmall),
				gui.Select(gui.SelectCfg{
					ID:       "currency_base_select",
					A11YCfg:  gui.A11YCfg{A11YLabel: "Базовая валюта"},
					Selected: []string{string(base)},
					Options:  currencyOptions,
					MinWidth: 120,
					MaxWidth: 120,
					OnSelect: viewDialogSettingsCurrenciesBaseHandler,
				}),
				gui.Label(
					"Правила начисления донатов задаются в этой валюте",
					theme.TextStyleSecondary,
				),
				gui.Label("Курсы", theme.TextStyleTitleSmall),
				gui.Column(gui.ContainerCfg{
					Sizing:  gui.FillFit,
					Padding: gui.NoPadding,
					Spacing: gui.SpacingSmall,
					Content: rows,
				}),
				gui.Button(gui.ButtonCfg{
					ID:       "currency_refresh_button",
					Variant:  gui.ButtonSecondary,
					Disabled: gState.SettingsState.Currencies.Pending,
					Content: []gui.View{
						gui.Label("Обновить курсы", theme.TextStyleBody),
					},
					OnClick: viewDialogSettingsCurrenciesRefreshHandler,
				}),
				gui.Label(
					currenciesStatusText(gState.SettingsState.Currencies),
					currenciesStatusStyle(theme, gState.SettingsState.Currencies),
				),
			},
		}),
	}
}

// currencyRateRow — строка курса одной валюты: «1 <код> = [поле] <база>».
// Для базовой валюты поле не редактируется.
func currencyRateRow(gState *state.AppState, base, code settings.Currency) gui.View {
	theme := gui.CurrentTheme()
	isBase := code == base

	rate := gState.SettingsState.Settings.Currencies[code]
	if isBase {
		rate = 1
	}

	// Ставки нет (курсы очищены) — поле пустое, а подсказку даёт строка
	// статуса раздела.
	text := ""
	if rate > 0 {
		text = formatCurrencyRate(rate)
	}

	return gui.Row(gui.ContainerCfg{
		VAlign:  gui.VAlignMiddle,
		Padding: gui.NoPadding,
		Content: []gui.View{
			gui.Label("1 "+string(code)+" =", theme.TextStyleBody),
			gui.Input(gui.InputCfg{
				ID:          "currency_rate_" + strings.ToLower(string(code)),
				Placeholder: "курс",
				Width:       90,
				Text:        text,
				Disabled:    isBase,
				A11YCfg: gui.A11YCfg{
					A11YLabel: "Курс " + string(code) + " к базовой валюте",
				},
				OnTextChanged: viewDialogSettingsCurrenciesRateHandler(code),
			}),
			gui.Label(string(base), theme.TextStyleBody),
		},
	})
}

// currenciesStatusText — строка статуса раздела: ошибка, подсказка или
// время последнего обновления курсов.
func currenciesStatusText(c state.CurrenciesState) string {
	switch {
	case c.Error != "":
		return "⚠ " + c.Error
	case c.Pending:
		return "Курсы обновляются…"
	case c.NeedsRefresh:
		return "Курсы не пересчитаны: обновите их из API"
	case !c.UpdatedAt.IsZero():
		return "Курсы обновлены " + c.UpdatedAt.Local().Format("02.01.2006 15:04")
	default:
		return "Курсы заданы вручную"
	}
}

// currenciesStatusStyle — роль темы для строки статуса.
func currenciesStatusStyle(theme gui.Theme, c state.CurrenciesState) gui.TextStyle {
	switch {
	case c.Error != "":
		return theme.TextStyleError
	case c.Pending, c.NeedsRefresh:
		return theme.TextStyleSecondary
	default:
		return theme.TextStyleBody
	}
}

// baseCurrency — базовая валюта из настроек; неизвестный код заменяется
// на RUB, чтобы раздел оставался рабочим после правки settings.json.
func baseCurrency(c settings.Currency) settings.Currency {
	if knownCurrency(c) {
		return c
	}
	return settings.CurrencyRUB
}

// knownCurrency — валюта из списка раздела (USD, EUR, RUB).
func knownCurrency(c settings.Currency) bool {
	for _, code := range currencyCodes {
		if code == c {
			return true
		}
	}
	return false
}

// allRatesPresent — есть ли курс у каждой валюты раздела.
func allRatesPresent(rates map[settings.Currency]float64) bool {
	for _, code := range currencyCodes {
		if rates[code] <= 0 {
			return false
		}
	}
	return true
}

// formatCurrencyRate — курс для поля ввода: без хвостовых нулей и
// экспоненты (80 → «80», 0.0125 → «0.0125»).
func formatCurrencyRate(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// parseCurrencyRate разбирает курс из поля ввода: число с запятой или
// точкой, строго больше нуля.
func parseCurrencyRate(s string) (float64, error) {
	v, err := strconv.ParseFloat(
		strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64,
	)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("курс должен быть числом больше 0")
	}
	return v, nil
}
