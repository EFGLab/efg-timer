package app

import (
	"strconv"

	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/go-gui-org/go-gui/gui"
)

func settingsDialogTwitchSection(w *gui.Window) []gui.View {
	gState := gui.State[state.AppState](w)
	theme := gui.CurrentTheme()
	auth := gState.SettingsState.TwitchAuth
	twitchCfg := gState.SettingsState.Settings.Providers.Twitch

	var toggleButton gui.View
	switch {
	case auth.Pending:
		toggleButton = gui.Button(gui.ButtonCfg{
			ID:      "twitch_cancel_button",
			Variant: gui.ButtonDanger,
			Content: []gui.View{
				gui.Label("Cancel", theme.TextStyleBody),
			},
			OnClick: viewDialogSettingsTwitchCancelHandler,
		})
	case twitchCfg.Enabled:
		toggleButton = gui.Button(gui.ButtonCfg{
			ID:      "twitch_disable_button",
			Variant: gui.ButtonDanger,
			Content: []gui.View{
				gui.Label("Disable", theme.TextStyleBody),
			},
			OnClick: viewDialogSettingsTwitchDisableHandler,
		})
	default:
		toggleButton = gui.Button(gui.ButtonCfg{
			ID:      "twitch_enable_button",
			Variant: gui.ButtonPrimary,
			Content: []gui.View{
				gui.Label("Enable", theme.TextStyleBody),
			},
			OnClick: viewDialogSettingsTwitchEnableHandler,
		})
	}

	status := make([]gui.View, 0, 3)
	switch {
	case auth.Pending:
		code := auth.UserCode
		if code == "" {
			code = "получаем код…"
		}
		status = append(status,
			gui.Label("Код для входа на Twitch: "+code, theme.TextStyleTitleSmall),
			gui.Button(gui.ButtonCfg{
				ID:      "twitch_open_button",
				Variant: gui.ButtonSecondary,
				Content: []gui.View{
					gui.Label("Открыть Twitch", theme.TextStyleBody),
				},
				OnClick: viewDialogSettingsTwitchOpenHandler,
			}),
		)
	case auth.Error != "":
		status = append(status, gui.Label("⚠ "+auth.Error, theme.TextStyleSecondary))
	case twitchCfg.Enabled:
		login := twitchCfg.Login
		if login == "" {
			login = "канал не получен"
		}
		status = append(status, gui.Label("Подключён: "+login, theme.TextStyleBody))
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
							Text:      "Twitch",
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
				gui.Label("Subscriptions", theme.TextStyleTitleSmall),
				gui.InputGroup(gui.InputGroupCfg{
					ID: "tier1",
					Segments: []gui.InputGroupSegment{
						gui.InputGroupText(gui.InputGroupTextCfg{
							Text:      "Tier 1",
							TextStyle: theme.TextStyleBody,
						}),
						gui.InputGroupInput(gui.InputCfg{
							ID:          "time-input",
							Placeholder: "time",
							// Подпись поля выводится только как A11Y-имя.
							A11YCfg:       gui.A11YCfg{A11YLabel: "Длительность Tier 1"},
							Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.Twitch.Tier1SubTime.Value)),
							OnTextChanged: viewDialogSettingsTwitchTier1SubValueHandler,
						}),
						gui.InputGroupSelect(gui.SelectCfg{
							ID:          "time-select",
							Placeholder: "time",
							A11YCfg:     gui.A11YCfg{A11YLabel: "Единица Tier 1"},
							MaxWidth:    120,
							MinWidth:    120,
							Selected: []string{
								string(gState.SettingsState.Settings.Providers.Twitch.Tier1SubTime.Unit),
							},
							Options:  timeUnitOptions,
							OnSelect: viewDialogSettingsTwitchTier1SubUnitHandler,
						}),
					},
				}),
				gui.InputGroup(gui.InputGroupCfg{
					ID: "tier2",
					Segments: []gui.InputGroupSegment{
						gui.InputGroupText(gui.InputGroupTextCfg{
							Text:      "Tier 2",
							TextStyle: theme.TextStyleBody,
						}),
						gui.InputGroupInput(gui.InputCfg{
							ID:            "time-input",
							Placeholder:   "time",
							A11YCfg:       gui.A11YCfg{A11YLabel: "Длительность Tier 2"},
							Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.Twitch.Tier2SubTime.Value)),
							OnTextChanged: viewDialogSettingsTwitchTier2SubValueHandler,
						}),
						gui.InputGroupSelect(gui.SelectCfg{
							ID:          "time-select",
							Placeholder: "time",
							A11YCfg:     gui.A11YCfg{A11YLabel: "Единица Tier 2"},
							MaxWidth:    120,
							MinWidth:    120,
							Selected: []string{
								string(gState.SettingsState.Settings.Providers.Twitch.Tier2SubTime.Unit),
							},
							Options:  timeUnitOptions,
							OnSelect: viewDialogSettingsTwitchTier2SubUnitHandler,
						}),
					},
				}),
				gui.InputGroup(gui.InputGroupCfg{
					ID: "tier3",
					Segments: []gui.InputGroupSegment{
						gui.InputGroupText(gui.InputGroupTextCfg{
							Text:      "Tier 3",
							TextStyle: theme.TextStyleBody,
						}),
						gui.InputGroupInput(gui.InputCfg{
							ID:            "time-input",
							Placeholder:   "time",
							A11YCfg:       gui.A11YCfg{A11YLabel: "Длительность Tier 3"},
							Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.Twitch.Tier3SubTime.Value)),
							OnTextChanged: viewDialogSettingsTwitchTier3SubValueHandler,
						}),
						gui.InputGroupSelect(gui.SelectCfg{
							ID:          "time-select",
							Placeholder: "time",
							A11YCfg:     gui.A11YCfg{A11YLabel: "Единица Tier 3"},
							MaxWidth:    120,
							MinWidth:    120,
							Selected: []string{
								string(gState.SettingsState.Settings.Providers.Twitch.Tier3SubTime.Unit),
							},
							Options:  timeUnitOptions,
							OnSelect: viewDialogSettingsTwitchTier3SubUnitHandler,
						}),
					},
				}),
				gui.Checkbox(gui.ToggleCfg{
					ID:       "twitch_count_resub",
					Label:    "Считать ре-сабы",
					Selected: gState.SettingsState.Settings.Providers.Twitch.CountResub,
					OnClick:  viewDialogSettingsTwitchCountResubHandler,
				}),
				gui.Label("Bits", theme.TextStyleTitleSmall),
				gui.Row(gui.ContainerCfg{
					VAlign:  gui.VAlignMiddle,
					Padding: gui.NoPadding,
					Content: []gui.View{
						gui.InputGroup(gui.InputGroupCfg{
							ID: "bits",
							Segments: []gui.InputGroupSegment{
								gui.InputGroupInput(gui.InputCfg{
									ID:            "time-input",
									Placeholder:   "10",
									A11YCfg:       gui.A11YCfg{A11YLabel: "Длительность битов"},
									Width:         50,
									Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.Twitch.BitsTime.Value)),
									OnTextChanged: viewDialogSettingsTwitchBitsValueHandler,
								}),
								gui.InputGroupSelect(gui.SelectCfg{
									ID:          "time-select",
									Placeholder: "time",
									A11YCfg:     gui.A11YCfg{A11YLabel: "Единица битов"},
									MaxWidth:    100,
									MinWidth:    100,
									Selected: []string{
										string(gState.SettingsState.Settings.Providers.Twitch.BitsTime.Unit),
									},
									Options:  timeUnitOptions,
									OnSelect: viewDialogSettingsTwitchBitsUnitHandler,
								}),
							},
						}),
						gui.Label("per", theme.TextStyleBody),
						gui.Input(gui.InputCfg{
							ID:            "bits-input",
							Placeholder:   "100",
							A11YCfg:       gui.A11YCfg{A11YLabel: "Битов за единицу времени"},
							Width:         50,
							Text:          strconv.Itoa(int(gState.SettingsState.Settings.Providers.Twitch.PerBitsAmount)),
							OnTextChanged: viewDialogSettingsTwitchBitsHandler,
						}),
						gui.Label("Bits", theme.TextStyleBody.Bold()),
					},
				}),
			}}),
	}
}
