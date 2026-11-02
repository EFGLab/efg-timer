package timer

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/EFGLab/efg-timer/internal/helpers"
)

type Source string

const (
	SourceManual         Source = "manual"
	SourceDonatex        Source = "donatex"
	SourceDonationalerts Source = "donationalerts"
	SourceTwitch         Source = "twitch"
)

type AuditOp string

const (
	OpAdd   AuditOp = "add"
	OpSet   AuditOp = "set"
	OpStart AuditOp = "start"
	OpStop  AuditOp = "stop"
	OpReset AuditOp = "reset"
)

type AuditEntry struct {
	At     time.Time
	Op     AuditOp
	Source Source
	Detail string
	Before time.Duration
	After  time.Duration
}

const auditCap = 200

type Timer struct {
	duration  time.Duration // initial duration
	remaining time.Duration

	ticker *helpers.PausableTicker

	mu sync.Mutex

	running bool // движок уже запущен (идемпотентность Run)

	tickSubs  []func(remaining time.Duration)
	auditSubs []func(audit AuditEntry)

	audit []AuditEntry // журнал мутаций (кольцевой буфер, auditCap)
}

func New(d time.Duration) *Timer {
	return &Timer{
		duration:  d,
		remaining: d,
		ticker:    helpers.NewPausableTicker(time.Second),
	}
}

// Run запускает движок таймера. Идемпотентен: повторный вызов — в том
// числе одновременный с первым (например, из onAppInit и из кнопки
// «Старт») — не поднимает второй тикер.
func (t *Timer) Run(ctx context.Context) {
	t.mu.Lock()
	if t.running || t.ticker.State() != helpers.Idle {
		// Движок уже запущен — выходим, работающий тикер не трогаем.
		t.mu.Unlock()
		return
	}
	t.running = true
	t.mu.Unlock()

	go t.ticker.Run(ctx)

	for range t.ticker.C() {
		t.mu.Lock()
		if t.remaining <= 0 {
			t.mu.Unlock()
			// Команда паузы неблокирующая: её безопасно звать из цикла
			// чтения тиков.
			t.ticker.Pause()
			// Уведомляем подписчиков после остановки: тиков больше не
			// будет, иначе окно узнает об остановке только по клику.
			t.notify()
			continue
		}

		t.remaining -= time.Second
		t.mu.Unlock()
		t.notify()
	}
}

func (t *Timer) notify() {
	t.mu.Lock()
	r := t.remaining
	subs := slices.Clone(t.tickSubs)
	t.mu.Unlock()

	for _, fn := range subs {
		if fn != nil {
			fn(r)
		}
	}
}

func (t *Timer) notifyAudit(audit AuditEntry) {
	t.mu.Lock()
	subs := slices.Clone(t.auditSubs)
	t.mu.Unlock()

	for _, fn := range subs {
		if fn != nil {
			fn(audit)
		}
	}
}

func (t *Timer) Resume(src Source) {
	t.mu.Lock()
	if t.ticker.State() == helpers.Running {
		t.mu.Unlock()
		return
	}

	t.ticker.Resume()
	entry := t.logAuditLocked(OpStart, src, "", t.remaining, t.remaining)
	t.mu.Unlock()

	// Рассылку делаем после снятия мьютекса: подписчик вправе снова позвать
	// таймер.
	t.notifyAudit(entry)
}

func (t *Timer) Pause(src Source) {
	t.mu.Lock()
	if t.ticker.State() != helpers.Running {
		t.mu.Unlock()
		return
	}

	t.ticker.Pause()
	entry := t.logAuditLocked(OpStop, src, "", t.remaining, t.remaining)
	t.mu.Unlock()

	t.notifyAudit(entry)
}

func (t *Timer) Reset(src Source) {
	t.mu.Lock()
	before := t.remaining
	t.remaining = t.duration

	t.ticker.Pause()

	entry := t.logAuditLocked(OpReset, src, "", before, t.duration)
	t.mu.Unlock()

	t.notifyAudit(entry)
}

func (t *Timer) SetRemaining(src Source, d time.Duration) {
	t.mu.Lock()
	before := t.remaining
	t.remaining = d

	entry := t.logAuditLocked(OpSet, src, "", before, d)
	t.mu.Unlock()

	t.notifyAudit(entry)
}

func (t *Timer) Add(src Source, d time.Duration, detail string) {
	t.mu.Lock()
	before := t.remaining
	t.remaining += d

	entry := t.logAuditLocked(OpAdd, src, detail, before, t.remaining)
	t.mu.Unlock()

	// Подписчики тиков уведомляются тоже: начисленное время должно сразу
	// появиться на счётчике (на паузе тиков нет вообще).
	t.notify()

	t.notifyAudit(entry)
}

func (t *Timer) Audit() []AuditEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.audit)
}

func (t *Timer) RestoreAudit(audit []AuditEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.audit = slices.Clone(audit)
}

// logAuditLocked добавляет запись в кольцевой буфер журнала. Вызывается под
// t.mu; рассылку подписчикам делает вызывающий (после снятия мьютекса) —
// иначе notifyAudit повторно захватил бы t.mu и вызов зависал.
func (t *Timer) logAuditLocked(op AuditOp, src Source, detail string, before, after time.Duration) AuditEntry {
	entry := AuditEntry{
		At:     time.Now(),
		Op:     op,
		Source: src,
		Detail: detail,
		Before: before,
		After:  after,
	}
	t.audit = append(t.audit, entry)
	if len(t.audit) > auditCap {
		t.audit = t.audit[len(t.audit)-auditCap:]
	}

	return entry
}

func (t *Timer) State() helpers.State {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ticker.State()
}

func (t *Timer) Duration() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.duration
}

func (t *Timer) Remaining() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.remaining
}

func (t *Timer) Subscribe(fn func(remaining time.Duration)) (unsubscribe func()) {
	t.mu.Lock()
	t.tickSubs = append(t.tickSubs, fn)
	i := len(t.tickSubs) - 1
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		t.tickSubs[i] = nil
		t.mu.Unlock()
	}
}

func (t *Timer) SubscribeAudit(fn func(audit AuditEntry)) (unsubscribe func()) {
	t.mu.Lock()
	t.auditSubs = append(t.auditSubs, fn)
	i := len(t.auditSubs) - 1
	t.mu.Unlock()
	return func() {
		t.mu.Lock()
		t.auditSubs[i] = nil
		t.mu.Unlock()
	}
}
