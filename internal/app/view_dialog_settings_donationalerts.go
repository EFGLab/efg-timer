package app

import (
	"strconv"

	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
)

func settingsDialogDonationAlertsSection(w *gui.Window) []gui.View {
	gState := gui.State[state.AppState](w)
	theme := gui.CurrentTheme()
	auth := gState.SettingsState.DonationAlertsAuth
	daCfg := gState.SettingsState.Settings.Providers.DonationAlerts

	var toggleButton gui.View
	switch {
	case auth.Pending:
		toggleButton = gui.Button(gui.ButtonCfg{
			ID:      "donationalerts_cancel_button",
			Variant: gui.ButtonDanger,
			Content: []gui.View{
				gui.Label("Cancel", theme.TextStyleBody),
			},
			OnClick: viewDialogSettingsDonationAlertsCancelHandler,
		})
	case daCfg.Enabled:
		toggleButton = gui.Button(gui.ButtonCfg{
			ID:      "donationalerts_disable_button",
			Variant: gui.ButtonDanger,
			Content: []gui.View{
				gui.Label("Disable", theme.TextStyleBody),
			},
			OnClick: viewDialogSettingsDonationAlertsDisableHandler,
		})
	default:
		toggleButton = gui.Button(gui.ButtonCfg{
			ID:      "donationalerts_enable_button",
			Variant: gui.ButtonPrimary,
			Content: []gui.View{
				gui.Label("Enable", theme.TextStyleBody),
			},
			OnClick: viewDialogSettingsDonationAlertsEnableHandler,
		})
	}

	status := make([]gui.View, 0, 2)
	switch {
	case auth.Pending:
		status = append(status,
			gui.Label("Вход в DonationAlerts…", theme.TextStyleTitleSmall),
			gui.Button(gui.ButtonCfg{
				ID:      "donationalerts_open_button",
				Variant: gui.ButtonSecondary,
				Content: []gui.View{
					gui.Label("Открыть DonationAlerts", theme.TextStyleBody),
				},
				OnClick: viewDialogSettingsDonationAlertsOpenHandler,
			}),
		)
	case auth.Error != "":
		status = append(status, gui.Label("⚠ "+auth.Error, theme.TextStyleError))
	case daCfg.Enabled:
		if daCfg.UserID != 0 {
			status = append(status, gui.Label("Подключён: "+strconv.FormatInt(daCfg.UserID, 10), theme.TextStyleBody))
		} else {
			status = append(status, gui.Label("Подключён", theme.TextStyleBody))
		}
	default:
		status = append(status, gui.Label("Не подключён", theme.TextStyleSecondary))
	}

	return []gui.View{
		gui.Column(gui.ContainerCfg{
			Sizing:  gui.FillFill,
			Padding: gui.PadVH(theme.PaddingMedium.Top, 0),
			Content: []gui.View{
				gui.Row(gui.ContainerCfg{
					Sizing:  gui.FillFit,
					VAlign:  gui.VAlignMiddle,
					Padding: gui.NoPadding,
					Content: []gui.View{
						gui.Text(gui.TextCfg{
							Sizing:    gui.FillFit,
							Text:      "DonationAlerts",
							TextStyle: theme.TextStyleTitle,
						}),
						toggleButton,
					},
				}),
				gui.Column(gui.ContainerCfg{
					Sizing:  gui.FillFit,
					Padding: gui.NoPadding,
					Spacing: gui.SpacingSmall,
					Content: status,
				}),
				gui.Label("Donations", theme.TextStyleTitleSmall),
				gui.Row(gui.ContainerCfg{
					VAlign:  gui.VAlignMiddle,
					Padding: gui.NoPadding,
					Content: []gui.View{
						gui.InputGroup(gui.InputGroupCfg{
							ID: "donationalerts-time",
							Segments: []gui.InputGroupSegment{
								gui.InputGroupInput(gui.InputCfg{
									ID:            "time-input",
									Placeholder:   "10",
									A11YCfg:       gui.A11YCfg{A11YLabel: "Длительность доната"},
									Width:         50,
									Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.DonationAlerts.Time.Value)),
									OnTextChanged: viewDialogSettingsDonationAlertsValueHandler,
								}),
								gui.InputGroupSelect(gui.SelectCfg{
									ID:          "time-select",
									Placeholder: "time",
									A11YCfg:     gui.A11YCfg{A11YLabel: "Единица доната"},
									MaxWidth:    100,
									MinWidth:    100,
									Selected: []string{
										string(gState.SettingsState.Settings.Providers.DonationAlerts.Time.Unit),
									},
									Options:  timeUnitOptions,
									OnSelect: viewDialogSettingsDonationAlertsUnitHandler,
								}),
							},
						}),
						gui.Label("per", theme.TextStyleBody),
						gui.InputGroup(gui.InputGroupCfg{
							ID: "donationalerts-amount",
							Segments: []gui.InputGroupSegment{
								gui.InputGroupInput(gui.InputCfg{
									ID:            "amount-input",
									Placeholder:   "100",
									A11YCfg:       gui.A11YCfg{A11YLabel: "Порог доната"},
									Width:         50,
									Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.DonationAlerts.PerAmount)),
									OnTextChanged: viewDialogSettingsDonationAlertsMoneyHandler,
								}),
							},
						}),
						gui.Label(
							string(baseCurrency(gState.SettingsState.Settings.BaseCurrency)),
							theme.TextStyleBody,
						),
					},
				}),
			},
		}),
	}
}
