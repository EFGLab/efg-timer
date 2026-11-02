package app

import (
	"strconv"

	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
)

// Идентичности собирает фрейм — scope группы InputGroup плюс короткий
// leaf. Leaf "time-input" здесь совпадает с leaf в разделе
// DonationAlerts, но scope у групп разный ("donatex-time" против
// "donationalerts-time"), поэтому эффективные ID в кадре не совпадают.
func settingsDialogDonatexSection(w *gui.Window) []gui.View {
	gState := gui.State[state.AppState](w)
	theme := gui.CurrentTheme()

	// Кнопка Enable переключает флаг в черновике настроек, как кнопка в
	// разделе DonationAlerts. Сам клиент DonateX в приложение ещё не
	// подключён, поэтому обработчик ничего не запускает (см.
	// viewDialogSettingsDonateXEnableHandler).
	var toggleButton gui.View
	if gState.SettingsState.Settings.Providers.DonateX.Enabled {
		toggleButton = gui.Button(gui.ButtonCfg{
			ID:      "donatex_disable_button",
			Variant: gui.ButtonDanger,
			Content: []gui.View{
				gui.Label("Disable", theme.TextStyleBody),
			},
			OnClick: viewDialogSettingsDonateXDisableHandler,
		})
	} else {
		toggleButton = gui.Button(gui.ButtonCfg{
			ID:      "donatex_enable_button",
			Variant: gui.ButtonPrimary,
			Content: []gui.View{
				gui.Label("Enable", theme.TextStyleBody),
			},
			OnClick: viewDialogSettingsDonateXEnableHandler,
		})
	}

	return []gui.View{
		gui.Column(gui.ContainerCfg{
			Padding: gui.PadVH(theme.PaddingMedium.Top, 0),
			Sizing:  gui.FillFill,
			Content: []gui.View{
				gui.Row(gui.ContainerCfg{
					Sizing:  gui.FillFit,
					VAlign:  gui.VAlignMiddle,
					Padding: gui.NoPadding,
					Content: []gui.View{
						gui.Text(gui.TextCfg{
							Sizing:    gui.FillFit,
							Text:      "DonateX",
							TextStyle: theme.TextStyleTitle,
						}),
						toggleButton,
					},
				}),
				gui.Label("Donations", theme.TextStyleTitleSmall),
				gui.Row(gui.ContainerCfg{
					VAlign:  gui.VAlignMiddle,
					Padding: gui.NoPadding,
					Content: []gui.View{
						gui.InputGroup(gui.InputGroupCfg{
							ID: "donatex-time",
							Segments: []gui.InputGroupSegment{
								gui.InputGroupInput(gui.InputCfg{
									ID:          "time-input",
									Placeholder: "10",
									// Подпись поля выводится только как A11Y-имя.
									A11YCfg:       gui.A11YCfg{A11YLabel: "Длительность доната"},
									Width:         50,
									Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.DonateX.Time.Value)),
									OnTextChanged: viewDialogSettingsDonateXValueHandler,
								}),
								gui.InputGroupSelect(gui.SelectCfg{
									ID:          "time-select",
									Placeholder: "time",
									A11YCfg:     gui.A11YCfg{A11YLabel: "Единица доната"},
									MaxWidth:    100,
									MinWidth:    100,
									Selected: []string{
										string(gState.SettingsState.Settings.Providers.DonateX.Time.Unit),
									},
									Options:  timeUnitOptions,
									OnSelect: viewDialogSettingsDonateXUnitHandler,
								}),
							},
						}),
						gui.Label("per", theme.TextStyleBody),
						gui.InputGroup(gui.InputGroupCfg{
							ID: "donatex-amount",
							Segments: []gui.InputGroupSegment{
								gui.InputGroupInput(gui.InputCfg{
									ID:            "amount-input",
									Placeholder:   "100",
									A11YCfg:       gui.A11YCfg{A11YLabel: "Порог доната"},
									Width:         50,
									Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.DonateX.PerAmount)),
									OnTextChanged: viewDialogSettingsDonateXMoneyHandler,
								}),
							},
						}),
					},
				}),
			},
		}),
	}
}
