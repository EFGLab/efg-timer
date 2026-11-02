package app

import (
	"strconv"
	"strings"

	"github.com/EFGLab/efg-timer/internal/timer"
)

const auditMaxRows = 100

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

func auditEntryKey(e timer.AuditEntry) string {
	return strconv.FormatInt(e.At.UnixNano(), 10) + "|" +
		string(e.Op) + "|" + string(e.Source) + "|" + e.Detail + "|" +
		strconv.FormatInt(e.Before.Nanoseconds(), 10) + "|" +
		strconv.FormatInt(e.After.Nanoseconds(), 10)
}
