package app

import (
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
)

// auditListID — ID виртуального списка журнала. Список прокручивается, а
// смещение прокрутки, модель высот и состояние фокуса ключуются ID.
const auditListID = "audit_log_list"

// auditEmptyText — заглушка пустого журнала.
const auditEmptyText = "Пока пусто — операции с таймером появятся здесь"

func auditDialogView(w *gui.Window) gui.View {
	theme := gui.CurrentTheme()
	state := gui.State[state.AppState](w)

	title := gui.Column(gui.ContainerCfg{
		Sizing: gui.FillFill, // занимает место слева, крестик уходит вправо
		Content: []gui.View{
			gui.Label("Журнал операций", theme.TextStyleLabel),
		},
	})
	header := gui.Row(gui.ContainerCfg{
		Sizing: gui.FillFill,
		Content: []gui.View{
			title,
			gui.Button(gui.ButtonCfg{
				ID: "audit_close",
				// У кнопки только иконка-глиф — доступное имя задаём явно.
				A11YCfg: gui.A11YCfg{A11YLabel: "Закрыть"},
				Content: []gui.View{
					gui.Text(gui.TextCfg{Text: gui.IconClose, TextStyle: theme.TextStyleIconMedium}),
				},
				OnClick: auditDialogViewCloseButtonHandler,
			}),
		},
	})

	// Тело журнала — виртуальный список: строки строятся только для
	// видимой части окна, а не для всех auditMaxRows записей.
	body := auditDialogBody(w, theme.TextStyleSecondary, theme.SpacingMedium, state.Timer.Audit())

	return gui.Column(gui.ContainerCfg{
		Sizing:  gui.FillFill,
		Content: []gui.View{header, body},
	})
}

// auditDialogBody — тело диалога журнала: виртуальный список последних
// записей или заглушка, пока записей нет.
//
// Список виртуализирован: ItemView вызывается только для строк, попавших
// в окно, поэтому FormatAuditEntry работает не для всех auditMaxRows
// записей, а только для видимых.
//
// gap — зазор между строками журнала (шаг темы). Собственный Spacing
// виртуального списка жёстко нулевой: зазор между строками — это высота,
// о которой модель высот не знает, поэтому зазор кладётся ВНУТРЬ строки
// (отступ снизу) и прибавляется к ItemHeight.
func auditDialogBody(w *gui.Window, style gui.TextStyle, gap float32, entries []timer.AuditEntry) gui.View {
	visible := auditVisible(entries)
	if len(visible) == 0 {
		return gui.Label(auditEmptyText, style)
	}

	// Высота строки известна заранее: строка журнала — одна строка
	// текста плюс отступ снизу (зазор до следующей строки).
	rowH := auditRowHeight(w, style) + gap

	return gui.VirtualList(gui.VirtualListCfg{
		ID:      auditListID,
		Sizing:  gui.FillFill,
		A11YCfg: gui.A11YCfg{A11YLabel: "Журнал операций"},
		// Столько строк в окне отображения: новые сверху.
		ItemCount: len(visible),
		// Высота строки задана явно: список ничего не измеряет сам, и
		// позиции строк точны с первого кадра.
		ItemHeight: func(_ int, _ float32) float32 { return rowH },
		// Ключ строки привязан к самой записи: ключ по позиции сдвинул бы
		// измеренную высоту на соседнюю строку.
		ItemKey: func(i int) string { return auditEntryKey(visible[i]) },
		// Ширину из аргумента в MinWidth не превращаем: список от этого
		// растёт каждый кадр.
		ItemView: func(i int, _ float32) gui.View {
			return gui.Column(gui.ContainerCfg{
				ID:         gui.ScopeIDN(auditListID, "row", i),
				Sizing:     gui.FitFit,
				SizeBorder: gui.NoBorder,
				// Зазор к следующей строке — внутри строки.
				Padding: gui.NewPadding(0, 0, gap, 0),
				Content: []gui.View{
					gui.Text(gui.TextCfg{
						Text:      FormatAuditEntry(visible[i]),
						TextStyle: style,
					}),
				},
			})
		},
	})
}

// auditRowHeight — высота строки журнала: одна строка текста в стиле
// TextStyleSecondary. Измеритель текста даёт точную высоту строки
// (ascent + descent). Без измерителя (headless-тесты) берётся та же
// оценка 1.4em, что использует библиотека.
func auditRowHeight(w *gui.Window, style gui.TextStyle) float32 {
	if style.Size <= 0 {
		// Тот же размер, что библиотека подставляет пустому стилю.
		style.Size = 14
	}
	if tm := w.TextMeasurer(); tm != nil {
		if h := tm.FontHeight(style); h > 0 {
			return h
		}
	}
	return style.Size * 1.4
}

func auditDialogViewCloseButtonHandler(ctx gui.EventCtx) {
	// Правило одного события: обработчик, который что-то делает, помечает
	// событие обработанным.
	ctx.Consume()
	ctx.Window.DialogDismiss()
}
