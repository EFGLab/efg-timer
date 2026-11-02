package app

import (
	"fmt"

	"github.com/EFGLab/efg-timer/internal/helpers"
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
)

func rootView(window *gui.Window) gui.View {
	state := gui.State[state.AppState](window)

	theme := gui.CurrentTheme()

	hint := gui.Label("", theme.TextStyleBody)
	if state.RootState.Error != "" {
		hint = gui.Label("⚠ "+state.RootState.Error, theme.TextStyleError)
	}

	okStyle, badStyle := theme.TextStyleSuccess, theme.TextStyleError
	var srvLine gui.View
	switch {
	case state.RootState.ServerStatus.Err != "":
		srvLine = gui.Label("⛔ Server: "+state.RootState.ServerStatus.Err, badStyle)
	case state.RootState.ServerStatus.Up:
		srvLine = gui.Label("● Server: started: "+state.RootState.ServerStatus.URL, okStyle)
	default:
		srvLine = gui.Label("○ Server: stopped", badStyle)
	}

	counterStyle := theme.TextStyleDisplay

	return gui.Column(gui.ContainerCfg{
		Sizing: gui.FillFill,
		HAlign: gui.HAlignCenter,
		Content: []gui.View{
			rootViewTopBar(window),
			gui.Label(FormatMMSS(state.RootState.Remaining), counterStyle),
			hint,
			srvLine,
			gui.Row(gui.ContainerCfg{
				HAlign: gui.HAlignCenter,
				Content: []gui.View{
					gui.TextButton(StateLabel(state.RootState.TimerState), rootViewPauseStartButtonHandler),
					gui.TextButton("Reset", rootViewResetButtonHandler),
				},
			}),
			gui.Row(gui.ContainerCfg{
				HAlign: gui.HAlignCenter,
				Content: []gui.View{
					gui.Input(gui.InputCfg{
						ID:            idDuration,
						Text:          state.RootState.TimeInput,
						Placeholder:   "MM:SS",
						Keyboard:      gui.KeyboardNumber,
						OnTextChanged: rootViewTimeInputHandler,
					}),
					gui.TextButton("Set", rootViewSetTimeButtonHandler),
				},
			}),
		},
	})
}

func rootViewTopBar(window *gui.Window) gui.View {
	theme := gui.CurrentTheme()

	return gui.Row(gui.ContainerCfg{
		Sizing: gui.FillFill,
		HAlign: gui.HAlignEnd,
		Content: []gui.View{
			gui.Button(gui.ButtonCfg{
				ID:      "audit_open",
				A11YCfg: gui.A11YCfg{A11YLabel: "Журнал операций"},
				Content: []gui.View{
					gui.Text(gui.TextCfg{Text: gui.IconClock, TextStyle: theme.TextStyleIconMedium}),
				},
				OnClick: rootViewAuditOpenButtonHandler,
			}),
			gui.Button(gui.ButtonCfg{
				ID:      "settings_open",
				A11YCfg: gui.A11YCfg{A11YLabel: "Настройки"},
				Content: []gui.View{
					gui.Text(gui.TextCfg{Text: gui.IconGear, TextStyle: theme.TextStyleIconMedium}),
				},
				OnClick: rootViewSettingsOpenButtonHandler,
			}),
		},
	})
}

func rootViewSettingsOpenButtonHandler(ctx gui.EventCtx) {
	ctx.Consume()

	state := gui.State[state.AppState](ctx.Window)

	var err error
	state.SettingsState.Settings, err = state.Settings.Copy()
	if err != nil {
		ctx.Window.Dialog(gui.DialogCfg{
			DialogType: gui.DialogMessage,
			Title:      "Error",
			Body:       fmt.Sprintf("Error init dialog window: %v", err),
		})
		return
	}

	ctx.Window.Dialog(gui.DialogCfg{
		DialogType: gui.DialogCustom,
		Width:      600,
		Height:     400,
		Title:      "Settings",
		CustomView: settingsDialogContent,
	})
}

func rootViewAuditOpenButtonHandler(ctx gui.EventCtx) {
	ctx.Consume()

	ctx.Window.Dialog(gui.DialogCfg{
		DialogType: gui.DialogCustom,
		Title:      "Журнал операций",
		ID:         "audit_dialog",
		Width:      560,
		Height:     460,
		CustomView: auditDialogView,
	})
}

func rootViewSetTimeButtonHandler(ctx gui.EventCtx) {
	ctx.Consume()

	state := gui.State[state.AppState](ctx.Window)
	d, err := ParseDurationMMSS(state.RootState.TimeInput)
	if err != nil {
		state.RootState.Error = err.Error()
		return
	}
	state.RootState.Error = ""

	state.Timer.SetRemaining(timer.SourceManual, d)

	publishRemaining(state.Timer.Remaining())
}

func rootViewTimeInputHandler(s string, ctx gui.EventCtx) {
	ctx.Consume()

	state := gui.State[state.AppState](ctx.Window)
	state.RootState.TimeInput = s
}

func rootViewPauseStartButtonHandler(ctx gui.EventCtx) {
	ctx.Consume()

	state := gui.State[state.AppState](ctx.Window)

	state.RootState.TimerState = state.Timer.State()

	switch state.RootState.TimerState {
	case helpers.Idle:
		state.Timer.Resume(timer.SourceManual)
	case helpers.Running:
		state.Timer.Pause(timer.SourceManual)
	case helpers.Paused:
		state.Timer.Resume(timer.SourceManual)
	}

	state.RootState.TimerState = state.Timer.State()
}

func rootViewResetButtonHandler(ctx gui.EventCtx) {
	ctx.Consume()

	state := gui.State[state.AppState](ctx.Window)

	state.Timer.Reset(timer.SourceManual)

	state.RootState.TimerState = state.Timer.State()
	publishRemaining(state.Timer.Remaining())
}
