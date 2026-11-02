package app

import (
	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
)

const auditListID = "audit_log_list"

const auditEmptyText = "Пока пусто — операции с таймером появятся здесь"

func auditDialogView(w *gui.Window) gui.View {
	theme := gui.CurrentTheme()
	state := gui.State[state.AppState](w)

	title := gui.Column(gui.ContainerCfg{
		Sizing: gui.FillFill,
		Content: []gui.View{
			gui.Label("Журнал операций", theme.TextStyleLabel),
		},
	})
	header := gui.Row(gui.ContainerCfg{
		Sizing: gui.FillFill,
		Content: []gui.View{
			title,
			gui.Button(gui.ButtonCfg{
				ID:      "audit_close",
				A11YCfg: gui.A11YCfg{A11YLabel: "Закрыть"},
				Content: []gui.View{
					gui.Text(gui.TextCfg{Text: gui.IconClose, TextStyle: theme.TextStyleIconMedium}),
				},
				OnClick: auditDialogViewCloseButtonHandler,
			}),
		},
	})

	body := auditDialogBody(w, theme.TextStyleSecondary, theme.SpacingMedium, state.Timer.Audit())

	return gui.Column(gui.ContainerCfg{
		Sizing:  gui.FillFill,
		Content: []gui.View{header, body},
	})
}

func auditDialogBody(w *gui.Window, style gui.TextStyle, gap float32, entries []timer.AuditEntry) gui.View {
	visible := auditVisible(entries)
	if len(visible) == 0 {
		return gui.Label(auditEmptyText, style)
	}

	rowH := auditRowHeight(w, style) + gap

	return gui.VirtualList(gui.VirtualListCfg{
		ID:         auditListID,
		Sizing:     gui.FillFill,
		A11YCfg:    gui.A11YCfg{A11YLabel: "Журнал операций"},
		ItemCount:  len(visible),
		ItemHeight: func(_ int, _ float32) float32 { return rowH },
		ItemKey:    func(i int) string { return auditEntryKey(visible[i]) },
		ItemView: func(i int, _ float32) gui.View {
			return gui.Column(gui.ContainerCfg{
				ID:         gui.ScopeIDN(auditListID, "row", i),
				Sizing:     gui.FitFit,
				SizeBorder: gui.NoBorder,
				Padding:    gui.NewPadding(0, 0, gap, 0),
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

func auditRowHeight(w *gui.Window, style gui.TextStyle) float32 {
	if style.Size <= 0 {
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
	ctx.Consume()
	ctx.Window.DialogDismiss()
}
