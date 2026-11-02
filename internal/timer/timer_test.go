package timer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/helpers"
)

// newFastTimer создаёт таймер с ускоренным тикером.
func newFastTimer(interval time.Duration) *Timer {
	tm := New(time.Hour)
	tm.ticker = helpers.NewPausableTicker(interval)
	return tm
}

// waitTickerState дожидается состояния тикера (опрос с шагом 1 мс).
func waitTickerState(t *testing.T, tm *Timer, want helpers.State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if tm.ticker.State() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("состояние тикера: %v, ожидалось %v", tm.ticker.State(), want)
}

// TestTimerRunIdempotent — одновременные вызовы Run не поднимают второй
// тикер: отсчёт идёт с обычной скоростью.
func TestTimerRunIdempotent(t *testing.T) {
	const (
		interval = 20 * time.Millisecond
		window   = 400 * time.Millisecond
	)

	tm := newFastTimer(interval)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticks := make(chan time.Duration, 512)
	tm.Subscribe(func(r time.Duration) { ticks <- r })

	// Три конкурентных Run — тот же сценарий, что и onAppInit + кнопка «Старт».
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tm.Run(ctx)
		}()
	}

	count := 0
	deadline := time.After(window)
loop:
	for {
		select {
		case <-ticks:
			count++
		case <-deadline:
			break loop
		}
	}

	// Один тикер за окно даёт window/interval ≈ 20 тиков; два тикера — 40.
	if count > 26 {
		t.Fatalf("тиков за %s: %d — отсчёт идёт быстрее обычного (запущено несколько тикеров)", window, count)
	}
	if count == 0 {
		t.Fatalf("тиков за %s: %d — таймер не тикает", window, count)
	}
}

// TestTimerRunSecondCallReturnsImmediately — повторный Run возвращается сразу
// и не ломает уже запущенный движок.
func TestTimerRunSecondCallReturnsImmediately(t *testing.T) {
	tm := newFastTimer(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tm.Run(ctx)
	waitTickerState(t, tm, helpers.Running)

	done := make(chan struct{})
	go func() {
		tm.Run(ctx) // повторный запуск
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("повторный Run не вернулся — движок запускается второй раз")
	}

	if st := tm.State(); st != helpers.Running {
		t.Fatalf("состояние таймера: %v, ожидалось Running", st)
	}
}

// TestTimerPauseResumeFromAnotherGoroutine — Pause/Resume из чужого потока
// не блокируют вызывающий.
func TestTimerPauseResumeFromAnotherGoroutine(t *testing.T) {
	tm := newFastTimer(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tm.Run(ctx)
	waitTickerState(t, tm, helpers.Running)

	done := make(chan struct{})
	go func() {
		tm.Pause(SourceManual)
		tm.Resume(SourceManual)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Pause/Resume заблокировали вызывающий поток")
	}
}

// TestTimerPauseStopsCountdown — пауза реально останавливает отсчёт,
// продолжение — возобновляет его.
func TestTimerPauseStopsCountdown(t *testing.T) {
	const interval = 20 * time.Millisecond

	tm := newFastTimer(interval)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticks := make(chan time.Duration, 512)
	tm.Subscribe(func(r time.Duration) { ticks <- r })

	go tm.Run(ctx)
	waitTickerState(t, tm, helpers.Running)

	// Первый тик — таймер идёт.
	select {
	case <-ticks:
	case <-time.After(2 * time.Second):
		t.Fatal("таймер не отсчитал ни секунды")
	}
	tm.Pause(SourceManual)
	waitTickerState(t, tm, helpers.Paused)

	// После паузы тиков быть не должно. Допускаем один «хвостовой» тик.
	paused := 0
	window := time.After(6 * interval)
loop:
	for {
		select {
		case <-ticks:
			paused++
			if paused > 1 {
				break loop
			}
		case <-window:
			break loop
		}
	}
	if paused > 1 {
		t.Fatalf("после Pause пришло %d тиков — отсчёт не остановился", paused)
	}

	// Сливаем «хвостовые» тики, чтобы проверка Resume ждала именно новый.
	drain(ticks)

	tm.Resume(SourceManual)
	waitTickerState(t, tm, helpers.Running)

	// После Resume тики снова идут.
	select {
	case <-ticks:
	case <-time.After(2 * time.Second):
		t.Fatal("после Resume таймер не возобновился")
	}
}

// drain убирает из канала всё, что уже пришло.
func drain(ch <-chan time.Duration) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// TestTimerAuditUnchangedByPauseResume — журнал операций продолжает писаться
// при паузе и продолжении.
func TestTimerAuditUnchangedByPauseResume(t *testing.T) {
	tm := newFastTimer(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go tm.Run(ctx)
	waitTickerState(t, tm, helpers.Running)

	tm.Pause(SourceManual)
	tm.Resume(SourceManual)
	tm.Pause(SourceManual)

	audit := tm.Audit()
	if len(audit) != 3 {
		t.Fatalf("записей в журнале: %d, ожидалось 3 (пауза, продолжение, пауза)", len(audit))
	}
	want := []AuditOp{OpStop, OpStart, OpStop}
	for i, op := range want {
		if audit[i].Op != op {
			t.Fatalf("операция %d в журнале: %v, ожидалось %v", i, audit[i].Op, op)
		}
	}
}

// TestTimerNotifiesWhenCountdownReachesZero — на нуле движок останавливает
// тикер и УВЕДОМЛЯЕТ подписчиков: иначе окно узнает об остановке только
// по клику, и надпись кнопки остаётся прежней.
func TestTimerNotifiesWhenCountdownReachesZero(t *testing.T) {
	// Движок уменьшает остаток на секунду за тик, поэтому исходное значение
	// должно быть ровно одной секундой — тогда ноль достигается точно.
	tm := New(time.Second)
	tm.ticker = helpers.NewPausableTicker(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticks := make(chan time.Duration, 64)
	tm.Subscribe(func(r time.Duration) { ticks <- r })

	go tm.Run(ctx)
	waitTickerState(t, tm, helpers.Running)

	// Ждём уведомления о нуле: значение остатка должно стать 0.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case r := <-ticks:
			if r == 0 {
				// Движок должен остановиться и больше не слать тиков.
				waitTickerState(t, tm, helpers.Paused)
				select {
				case extra := <-ticks:
					if extra > 0 {
						t.Fatalf("после остановки пришёл тик %v — отсчёт не остановлен", extra)
					}
				case <-time.After(100 * time.Millisecond):
				}
				return
			}
		case <-deadline:
			t.Fatal("движок не уведомил о достижении нуля")
		}
	}
}

// TestTimerRunReturnsWhenTickerStops — движок завершается, когда тикер
// остановлен (контекст отменён).
func TestTimerRunReturnsWhenTickerStops(t *testing.T) {
	tm := newFastTimer(20 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		tm.Run(ctx)
		close(done)
	}()

	waitTickerState(t, tm, helpers.Running)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run не завершился после отмены контекста")
	}
}
