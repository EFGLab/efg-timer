package app

import (
	"math"
	"strings"
	"testing"

	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
)

// Тесты раздела «Валюты».
//
// Проверяем то, что легко сломать незаметно: набор идентичностей раздела,
// пересчёт курсов при смене базовой валюты и разбор значения поля курса
// (запятая, точка, мусор).

// newCurrenciesWindow — окно с разделом «Валюты» и черновиком настроек.
func newCurrenciesWindow(t *testing.T) (*gui.Window, *state.AppState) {
	t.Helper()

	st := state.InitialAppState()
	draft, err := st.Settings.Copy()
	if err != nil {
		t.Fatalf("копия настроек: %v", err)
	}
	st.SettingsState.Settings = draft
	st.SettingsState.CurrentSection = state.SettingSectionCurrencies

	w := gui.NewTestWindow(gateCleanuper{t: t}, gui.WindowCfg{
		AppInfo: info,
		State:   st,
		Width:   800,
		Height:  600,
	})

	return w, st
}

// almostEqual — сравнение чисел с допуском: курсы считаются делением.
func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// Раздел строится: выбор базовой валюты, строка курса на каждую валюту и
// кнопка обновления. Идентичности — уникальные.
func TestCurrenciesSectionBuilds(t *testing.T) {
	w, _ := newCurrenciesWindow(t)

	root := w.TestRender(func(w *gui.Window) gui.View {
		return gui.Column(gui.ContainerCfg{
			Sizing:  gui.FillFill,
			Content: settingsDialogCurrenciesSection(w),
		})
	})

	for _, id := range []string{
		"currency_base_select",
		"currency_rate_usd",
		"currency_rate_eur",
		"currency_rate_rub",
		"currency_refresh_button",
	} {
		if _, ok := root.FindByID(id); !ok {
			t.Errorf("в разделе «Валюты» нет виджета %q", id)
		}
	}

	if dup := w.TestFindings(gui.DebugDuplicates); len(dup) > 0 {
		t.Errorf("дубли идентичностей в разделе «Валюты»:\n%s",
			strings.Join(dup, "\n"))
	}
}

// Смена базовой валюты пересчитывает курсы: новая база — 1, остальные
// курсы делятся на ставку новой базы.
func TestCurrenciesBaseChangeReanchorsRates(t *testing.T) {
	s := settings.Default()
	s.BaseCurrency = settings.CurrencyRUB
	s.Currencies = map[settings.Currency]float64{
		settings.CurrencyRUB: 1,
		settings.CurrencyUSD: 80,
		settings.CurrencyEUR: 88,
	}

	if !s.ReanchorCurrencyRates(settings.CurrencyUSD) {
		t.Fatal("пересчёт курсов под USD не удался")
	}

	if got := s.Currencies[settings.CurrencyUSD]; got != 1 {
		t.Errorf("курс базовой валюты = %v, ждали 1", got)
	}
	if got := s.Currencies[settings.CurrencyRUB]; !almostEqual(got, 1.0/80.0) {
		t.Errorf("курс RUB = %v, ждали %v", got, 1.0/80.0)
	}
	if got := s.Currencies[settings.CurrencyEUR]; !almostEqual(got, 88.0/80.0) {
		t.Errorf("курс EUR = %v, ждали %v", got, 88.0/80.0)
	}
}

// Пересчитать нечем — ставки новой базы нет.
func TestCurrenciesReanchorWithoutRateFails(t *testing.T) {
	s := settings.Default()
	s.BaseCurrency = settings.CurrencyRUB
	s.Currencies = map[settings.Currency]float64{
		settings.CurrencyRUB: 1,
	}

	if s.ReanchorCurrencyRates(settings.CurrencyUSD) {
		t.Fatal("пересчёт без ставки новой базы должен провалиться")
	}
}

// Смена базы в черновике: курсы пересчитаны, подсказка не нужна.
func TestCurrenciesBaseChangeUpdatesDraft(t *testing.T) {
	w, st := newCurrenciesWindow(t)
	st.SettingsState.Settings.BaseCurrency = settings.CurrencyRUB
	st.SettingsState.Settings.Currencies = map[settings.Currency]float64{
		settings.CurrencyRUB: 1,
		settings.CurrencyUSD: 80,
		settings.CurrencyEUR: 88,
	}

	ev := &gui.Event{Type: gui.EventMouseDown}
	ctx := gui.EventCtx{Window: w, Event: ev}
	viewDialogSettingsCurrenciesBaseHandler([]string{string(settings.CurrencyUSD)}, ctx)

	if !ev.IsHandled {
		t.Fatal("обработчик смены базы не пометил событие обработанным")
	}

	draft := st.SettingsState.Settings
	if draft.BaseCurrency != settings.CurrencyUSD {
		t.Errorf("базовая валюта = %q, ждали USD", draft.BaseCurrency)
	}
	if got := draft.Currencies[settings.CurrencyUSD]; got != 1 {
		t.Errorf("курс базовой валюты = %v, ждали 1", got)
	}
	if got := draft.Currencies[settings.CurrencyRUB]; !almostEqual(got, 1.0/80.0) {
		t.Errorf("курс RUB = %v, ждали %v", got, 1.0/80.0)
	}
	if st.SettingsState.Currencies.NeedsRefresh {
		t.Error("курсы пересчитаны — подсказка «обновите курсы» не нужна")
	}
}

// Пересчитать нечем: курсы очищаются, раздел просит обновить их из API.
func TestCurrenciesBaseChangeWithoutRateClearsRates(t *testing.T) {
	w, st := newCurrenciesWindow(t)
	st.SettingsState.Settings.BaseCurrency = settings.CurrencyRUB
	st.SettingsState.Settings.Currencies = map[settings.Currency]float64{
		settings.CurrencyRUB: 1,
	}

	ev := &gui.Event{Type: gui.EventMouseDown}
	ctx := gui.EventCtx{Window: w, Event: ev}
	viewDialogSettingsCurrenciesBaseHandler([]string{string(settings.CurrencyUSD)}, ctx)

	draft := st.SettingsState.Settings
	if draft.BaseCurrency != settings.CurrencyUSD {
		t.Errorf("базовая валюта = %q, ждали USD", draft.BaseCurrency)
	}
	if len(draft.Currencies) != 1 || draft.Currencies[settings.CurrencyUSD] != 1 {
		t.Errorf("курсы = %v, ждали только базу = 1", draft.Currencies)
	}
	if !st.SettingsState.Currencies.NeedsRefresh {
		t.Error("раздел должен просить обновить курсы из API")
	}
}

// Повторный выбор той же базы не теряет подсказку: курсов всех валют нет —
// значит, обновить их надо.
func TestCurrenciesBaseChangeKeepsRefreshHint(t *testing.T) {
	w, st := newCurrenciesWindow(t)
	st.SettingsState.Settings.BaseCurrency = settings.CurrencyRUB
	st.SettingsState.Settings.Currencies = map[settings.Currency]float64{
		settings.CurrencyRUB: 1,
	}

	ev := &gui.Event{Type: gui.EventMouseDown}
	ctx := gui.EventCtx{Window: w, Event: ev}
	viewDialogSettingsCurrenciesBaseHandler([]string{string(settings.CurrencyRUB)}, ctx)

	if !st.SettingsState.Currencies.NeedsRefresh {
		t.Error("курсов не хватает — подсказка «обновите курсы» нужна")
	}
}

// Разбор курса: запятая и точка равны, мусор и неположительные значения
// не принимаются.
func TestParseCurrencyRate(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{in: "80", want: 80, ok: true},
		{in: " 88 ", want: 88, ok: true},
		{in: "0,0125", want: 0.0125, ok: true},
		{in: "0.0125", want: 0.0125, ok: true},
		{in: "abc"},
		{in: ""},
		{in: "0"},
		{in: "-5"},
		{in: "1,2,3"},
	}

	for _, c := range cases {
		got, err := parseCurrencyRate(c.in)
		if !c.ok {
			if err == nil {
				t.Errorf("parseCurrencyRate(%q) = %v, ждали ошибку", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseCurrencyRate(%q) — ошибка %v", c.in, err)
			continue
		}
		if !almostEqual(got, c.want) {
			t.Errorf("parseCurrencyRate(%q) = %v, ждали %v", c.in, got, c.want)
		}
	}
}

// Правка курса вручную: годное значение принимается, мусор и неположительные
// значения — нет (поле остаётся с прежним курсом).
func TestCurrenciesRateHandlerAcceptsOnlyPositiveNumbers(t *testing.T) {
	w, st := newCurrenciesWindow(t)
	st.SettingsState.Settings.Currencies = map[settings.Currency]float64{
		settings.CurrencyRUB: 1,
		settings.CurrencyUSD: 80,
	}

	handler := viewDialogSettingsCurrenciesRateHandler(settings.CurrencyUSD)
	ev := &gui.Event{Type: gui.EventMouseDown}
	ctx := gui.EventCtx{Window: w, Event: ev}

	for _, bad := range []string{"abc", "", "0", "-5"} {
		handler(bad, ctx)
		if got := st.SettingsState.Settings.Currencies[settings.CurrencyUSD]; got != 80 {
			t.Fatalf("курс USD после %q = %v, ждали прежние 80", bad, got)
		}
	}

	handler("0,0125", ctx)
	if got := st.SettingsState.Settings.Currencies[settings.CurrencyUSD]; !almostEqual(got, 0.0125) {
		t.Errorf("курс USD = %v, ждали 0.0125", got)
	}
}

// Строка статуса: ошибка, подсказка, время обновления, ручные курсы.
func TestCurrenciesStatusText(t *testing.T) {
	cases := []struct {
		state state.CurrenciesState
		want  string
	}{
		{state: state.CurrenciesState{}, want: "Курсы заданы вручную"},
		{
			state: state.CurrenciesState{Pending: true},
			want:  "Курсы обновляются…",
		},
		{
			state: state.CurrenciesState{NeedsRefresh: true},
			want:  "Курсы не пересчитаны: обновите их из API",
		},
		{
			state: state.CurrenciesState{Error: "сеть недоступна"},
			want:  "⚠ сеть недоступна",
		},
	}

	for _, c := range cases {
		if got := currenciesStatusText(c.state); got != c.want {
			t.Errorf("currenciesStatusText(%+v) = %q, ждали %q", c.state, got, c.want)
		}
	}
}
