package app

import (
	"strconv"
	"strings"

	"github.com/EFGLab/efg-timer/internal/timer"
)

// auditMaxRows — сколько последних записей показывается в диалоге журнала
// (новые сверху). Это потолок окна просмотра, а не замена прокрутке.
const auditMaxRows = 100

// OpLabel — метка типа операции для журнала. Неизвестные значения
// возвращаются как есть — журнал не должен молча терять записи.
func OpLabel(op timer.AuditOp) string {
	switch op {
	case timer.OpAdd:
		return "добавление"
	case timer.OpSet:
		return "установка"
	case timer.OpStart:
		return "запуск"
	case timer.OpStop:
		return "остановка"
	case timer.OpReset:
		return "сброс"
	}
	return string(op)
}

// SourceLabel — метка источника операции. Неизвестные значения
// возвращаются как есть (см. OpLabel).
func SourceLabel(src timer.Source) string {
	switch src {
	case timer.SourceManual:
		return "вручную"
	case timer.SourceDonatex:
		return "донат Donatex"
	case timer.SourceDonationalerts:
		return "донат DonationAlerts"
	case timer.SourceTwitch:
		return "подписка Twitch"
	}
	return string(src)
}

// FormatAuditEntry — строка записи журнала:
// «21:07:05 · добавление · донат Donatex (150 ₽) · 05:00 → 06:00».
// Пустая Detail не оставляет пустых секций и «дыр» в разделителях.
func FormatAuditEntry(e timer.AuditEntry) string {
	var sb strings.Builder
	sb.WriteString(e.At.Format("15:04:05"))
	sb.WriteString(" · ")
	sb.WriteString(OpLabel(e.Op))
	sb.WriteString(" · ")
	sb.WriteString(SourceLabel(e.Source))
	if e.Detail != "" {
		sb.WriteString(" (")
		sb.WriteString(e.Detail)
		sb.WriteString(")")
	}
	sb.WriteString(" · ")
	sb.WriteString(FormatMMSS(e.Before))
	sb.WriteString(" → ")
	sb.WriteString(FormatMMSS(e.After))
	return sb.String()
}

// auditVisible — окно отображения: последние auditMaxRows записей,
// перевёрнутые (новые сверху) — исходный срез Audit() хронологический.
func auditVisible(entries []timer.AuditEntry) []timer.AuditEntry {
	if len(entries) > auditMaxRows {
		entries = entries[len(entries)-auditMaxRows:]
	}
	out := make([]timer.AuditEntry, len(entries))
	for i, e := range entries {
		out[len(entries)-1-i] = e
	}
	return out
}

// auditEntryKey — ключ записи журнала для модели высот виртуального списка.
//
// Ключ привязан к самой записи, а не к её позиции: новые записи вставляются
// сверху, и ключ по позиции заставил бы go-gui считать, что строка
// «переехала» на соседнюю — измеренная высота поехала бы за строкой.
func auditEntryKey(e timer.AuditEntry) string {
	return strconv.FormatInt(e.At.UnixNano(), 10) + "|" +
		string(e.Op) + "|" + string(e.Source) + "|" + e.Detail + "|" +
		strconv.FormatInt(e.Before.Nanoseconds(), 10) + "|" +
		strconv.FormatInt(e.After.Nanoseconds(), 10)
}
