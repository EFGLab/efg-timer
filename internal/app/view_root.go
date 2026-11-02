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
		// Подсказка об ошибке — роль темы TextStyleError.
		hint = gui.Label("⚠ "+state.RootState.Error, theme.TextStyleError)
	}

	// Статусная строка сервера: цвета берём из ролей темы.
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

	// Счётчик таймера — роль Display темы: размер шрифта задаёт тема.
	counterStyle := theme.TextStyleDisplay

	return gui.Column(gui.ContainerCfg{
		Sizing: gui.FillFill,
		HAlign: gui.HAlignCenter,
		Content: []gui.View{
			rootViewTopBar(window),
			// Остаток и состояние — из состояния окна: их публикует
			// gui.Stream, вьюха движок не дёргает.
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
						ID:          idDuration,
						Text:        state.RootState.TimeInput,
						Placeholder: "MM:SS",
						// Маски у поля нет: маска в go-gui разбивает ввод
						// по слотам (поле становится позиционным — «5:30»
						// превращается в «53:0»), а ParseDurationMMSS
						// принимает свободный текст «M:SS».
						//
						// Keyboard — только подсказка клавиатуры, набор
						// символов она не ограничивает.
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
			// Иконочные кнопки не имеют собственного текста, поэтому
			// доступное имя задаём явно.
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
	// Обработчик действует по событию — помечаем его обработанным.
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

	// Остаток в окне обновляем публикацией, а не прямой записью: значения
	// применяются в порядке поступления, поэтому отложенный тик движка не
	// перезапишет заданное вручную значение.
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

	// Состояние кнопки берём из движка — он источник истины: он сам
	// останавливает тикер, когда остаток дошёл до нуля, и об этом не
	// уведомляет.
	state.RootState.TimerState = state.Timer.State()

	switch state.RootState.TimerState {
	case helpers.Idle:
		// Движок уже запущен в onAppInit (go state.Timer.Run(w.Ctx())) —
		// второй запуск отсюда убран, иначе тикер пошёл бы двумя
		// потоками и отсчёт ускорялся вдвое. В состоянии Idle тикер ещё
		// не вошёл в цикл, поэтому «старт» — это тот же Resume, что и в
		// ветке Paused.
		state.Timer.Resume(timer.SourceManual)
	case helpers.Running:
		state.Timer.Pause(timer.SourceManual)
	case helpers.Paused:
		state.Timer.Resume(timer.SourceManual)
	}

	// В окно — состояние по факту, после действия.
	state.RootState.TimerState = state.Timer.State()
}

func rootViewResetButtonHandler(ctx gui.EventCtx) {
	ctx.Consume()

	state := gui.State[state.AppState](ctx.Window)

	state.Timer.Reset(timer.SourceManual)

	// Сброс возвращает полную длительность и останавливает тикер — оба
	// значения публикуем в окно.
	state.RootState.TimerState = state.Timer.State()
	publishRemaining(state.Timer.Remaining())
}
