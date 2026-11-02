package app

import (
	"github.com/EFGLab/efg-timer/internal/helpers"
	"github.com/EFGLab/efg-timer/internal/settings"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
)

type sidebarItem struct {
	id      string
	icon    string
	section state.SettingSection
	handler func(ctx gui.EventCtx)
	a11y    string
}

var sidebarItems []sidebarItem = []sidebarItem{
	{
		id:      "settings_nav_twitch",
		icon:    "assets/twitch.svg",
		section: state.SettingSectionTwitch,
		handler: settingsDialogViewTwitchSectionHandler,
		a11y:    "Настройки Twitch",
	},
	{
		id:      "settings_nav_donationalerts",
		icon:    "assets/donationalerts.svg",
		section: state.SettingSectionDonationAlerts,
		handler: settingsDialogViewDonationAlertsSectionHandler,
		a11y:    "Настройки DonationAlerts",
	},
	{
		id:      "settings_nav_donatex",
		icon:    "assets/donatex.svg",
		section: state.SettingSectionDonateX,
		handler: settingsDialogViewDonateXSectionHandler,
		a11y:    "Настройки DonateX",
	},
	{
		id:      "settings_nav_currencies",
		icon:    "assets/currencies.svg",
		section: state.SettingSectionCurrencies,
		handler: settingsDialogViewCurrenciesSectionHandler,
		a11y:    "Настройки валют",
	},
}

var timeUnitOptions []gui.SelectOption = []gui.SelectOption{
	{
		Label: "seconds",
		Value: string(settings.TimeUnitSecond),
	},
	{
		Label: "minutes",
		Value: string(settings.TimeUnitMinute),
	},
	{
		Label: "hours",
		Value: string(settings.TimeUnitHour),
	},
}

func settingsDialogContent(w *gui.Window) gui.View {
	st := gui.State[state.AppState](w)
	theme := gui.CurrentTheme()

	sidebarItemsView := make([]gui.View, 0, len(sidebarItems))
	for _, item := range sidebarItems {
		colorsSet := gui.ColorSet{}

		if st.SettingsState.CurrentSection == item.section {
			colorsSet.Base = theme.ColorActive
		}

		sidebarItemsView = append(sidebarItemsView, gui.Button(gui.ButtonCfg{
			ID:      item.id,
			Variant: gui.ButtonGhost,
			Padding: gui.PadAll(theme.SpacingMedium),
			A11YCfg: gui.A11YCfg{A11YLabel: item.a11y},
			Content: []gui.View{
				gui.Svg(gui.SvgCfg{
					ID:      "svg-circle",
					Width:   25,
					Height:  25,
					Sizing:  gui.FixedFixed,
					SvgData: string(helpers.ReadAssetContent(assetsFS, item.icon)),
				}),
			},
			OnClick: item.handler,
			Colors:  colorsSet,
		}))
	}

	sidebar := gui.Column(gui.ContainerCfg{
		Color:   theme.ColorBackground,
		HAlign:  gui.HAlignStart,
		Padding: gui.PaddingSmall,
		Spacing: gui.SpacingSmall,
		Sizing:  gui.FitFill,
		Content: sidebarItemsView,
	})

	var content []gui.View
	switch st.SettingsState.CurrentSection {
	case state.SettingSectionTwitch:
		content = settingsDialogTwitchSection(w)
	case state.SettingSectionDonationAlerts:
		content = settingsDialogDonationAlertsSection(w)
	case state.SettingSectionDonateX:
		content = settingsDialogDonatexSection(w)
	case state.SettingSectionCurrencies:
		content = settingsDialogCurrenciesSection(w)
	}

	contentView := gui.Column(gui.ContainerCfg{
		ID:         "settings_content",
		Color:      theme.ColorBackground,
		Padding:    gui.PadVH(0, theme.PaddingMedium.Left),
		Sizing:     gui.FillFill,
		Scrollable: true,
		ScrollMode: gui.ScrollVerticalOnly,
		Content:    content,
	})

	return gui.Column(gui.ContainerCfg{
		Sizing:  gui.FillFill,
		Padding: gui.NoPadding,
		Content: []gui.View{
			gui.Row(gui.ContainerCfg{
				Padding: gui.NoPadding,
				Sizing:  gui.FillFill,
				Spacing: gui.SpacingPx(theme.SpacingSmall),
				Content: []gui.View{
					sidebar,
					contentView,
				},
			}),
			gui.Row(gui.ContainerCfg{
				Sizing:  gui.FillFit,
				HAlign:  gui.HAlignRight,
				Padding: gui.NoPadding,
				Content: []gui.View{
					gui.Button(gui.ButtonCfg{
						ID: "settings_save",
						Content: []gui.View{
							gui.Text(gui.TextCfg{Text: "Save"}),
						},
						OnClick: viewDialogSettingsSaveSettingsHandler,
					}),
					gui.Button(gui.ButtonCfg{
						ID: "settings_cancel",
						Content: []gui.View{
							gui.Text(gui.TextCfg{Text: "Cancel"}),
						},
						OnClick: dialogDismiss,
					}),
				},
			}),
		},
	})

}
