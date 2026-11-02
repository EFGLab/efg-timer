package helpers

import (
	"context"
	"sync"
	"testing"
	"time"
)

// waitState дожидается нужного состояния тикера (опрос с шагом 1 мс).
func waitState(t *testing.T, pt *PausableTicker, want State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if pt.State() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("состояние тикера: %v, ожидалось %v", pt.State(), want)
}

// waitTick читает один тик; тест падает, если тик не пришёл.
func waitTick(t *testing.T, pt *PausableTicker) {
	t.Helper()
	select {
	case <-pt.C():
	case <-time.After(2 * time.Second):
		t.Fatal("ожидался тик от тикера")
	}
}

// TestPausableTickerRunIdempotent — несколько одновременных Run не
// поднимают несколько тикеров.
func TestPausableTickerRunIdempotent(t *testing.T) {
	const (
		interval = 20 * time.Millisecond
		window   = 400 * time.Millisecond
	)

	pt := NewPausableTicker(interval)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Все вызовы Run идут одновременно — самый неблагоприятный порядок.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pt.Run(ctx)
		}()
	}

	count := 0
	deadline := time.After(window)
loop:
	for {
		select {
		case <-pt.C():
			count++
		case <-deadline:
			break loop
		}
	}

	// Один тикер за окно даёт window/interval ≈ 20 тиков; четыре тикера — 80.
	// Запас сверху: 26 тиков.
	if count > 26 {
		t.Fatalf("тиков за %s: %d — тиков больше, чем у одного тикера (запущено несколько тикеров)", window, count)
	}
	if count == 0 {
		t.Fatalf("тиков за %s: %d — тикер не тикает", window, count)
	}
}

// TestPausableTickerSecondRunReturnsImmediately — повторный Run возвращается
// сразу и не ломает уже работающий тикер.
func TestPausableTickerSecondRunReturnsImmediately(t *testing.T) {
	pt := NewPausableTicker(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pt.Run(ctx)
	waitState(t, pt, Running)

	done := make(chan struct{})
	go func() {
		pt.Run(ctx) // повторный запуск
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("повторный Run не вернулся — тикер запускается второй раз")
	}

	// Уже работающий тикер не сломан: тики продолжают идти.
	waitTick(t, pt)
	waitState(t, pt, Running)
}

// TestPausableTickerPauseDoesNotBlock — Pause не блокирует вызывающий поток,
// даже если горутина тикера занята отправкой тика.
func TestPausableTickerPauseDoesNotBlock(t *testing.T) {
	pt := NewPausableTicker(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pt.Run(ctx)
	waitTick(t, pt)

	// Тики больше не читаем: горутина тикера упрётся в отправку.
	time.Sleep(30 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		pt.Pause()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Pause заблокировал вызывающий поток")
	}

	if st := pt.State(); st != Paused {
		t.Fatalf("состояние после Pause: %v, ожидалось Paused", st)
	}
}

// TestPausableTickerResumeDoesNotBlock — Resume не блокирует вызывающий поток.
func TestPausableTickerResumeDoesNotBlock(t *testing.T) {
	pt := NewPausableTicker(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pt.Run(ctx)
	waitState(t, pt, Running)
	pt.Pause()
	waitState(t, pt, Paused)

	// Тики не читаем: горутина тикера может быть занята отправкой.
	time.Sleep(30 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		pt.Resume()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Resume заблокировал вызывающий поток")
	}

	if st := pt.State(); st != Running {
		t.Fatalf("состояние после Resume: %v, ожидалось Running", st)
	}
}

// TestPausableTickerRepeatedPauseResume — повторные Pause/Resume безопасны:
// не блокируют, состояние остаётся корректным.
func TestPausableTickerRepeatedPauseResume(t *testing.T) {
	pt := NewPausableTicker(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pt.Run(ctx)
	waitState(t, pt, Running)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			pt.Pause()
			pt.Pause()
			pt.Resume()
			pt.Resume()
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("серия Pause/Resume заблокировала вызывающий поток")
	}

	if st := pt.State(); st != Running {
		t.Fatalf("состояние после серии Pause/Resume: %v, ожидалось Running", st)
	}

	// Тикер живой: тики продолжают идти.
	waitTick(t, pt)
}

// TestPausableTickerStopFinishesGoroutine — Stop дожидается завершения
// горутины тикера и закрывает канал тиков.
func TestPausableTickerStopFinishesGoroutine(t *testing.T) {
	pt := NewPausableTicker(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pt.Run(ctx)
	waitState(t, pt, Running)

	stopped := make(chan struct{})
	go func() {
		pt.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop не дождался завершения горутины тикера")
	}

	if st := pt.State(); st != Stopped {
		t.Fatalf("состояние после Stop: %v, ожидалось Stopped", st)
	}

	// Канал тиков закрыт — потребитель выходит из цикла чтения.
	select {
	case _, ok := <-pt.C():
		if ok {
			t.Fatal("после Stop ожидался закрытый канал тиков")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("канал тиков не закрыт после Stop")
	}
}

// TestPausableTickerStopWithoutRun — Stop на не запущенном тикере не зависает.
func TestPausableTickerStopWithoutRun(t *testing.T) {
	pt := NewPausableTicker(10 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		pt.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop на не запущенном тикере завис")
	}
}

// TestPausableTickerContextCancel — отмена контекста завершает горутину тикера.
func TestPausableTickerContextCancel(t *testing.T) {
	pt := NewPausableTicker(10 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	go pt.Run(ctx)
	waitState(t, pt, Running)
	cancel()

	select {
	case _, ok := <-pt.C():
		if ok {
			t.Fatal("после отмены контекста ожидался закрытый канал тиков")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("горутина тикера не завершилась после отмены контекста")
	}

	if st := pt.State(); st != Stopped {
		t.Fatalf("состояние после отмены контекста: %v, ожидалось Stopped", st)
	}
}

// TestPausableTickerStateConcurrentAccess — состояние тикера читается и
// меняется из разных горутин (прежняя версия писала state без мьютекса —
// data race; -race локально недоступен, поэтому проверяем значения).
func TestPausableTickerStateConcurrentAccess(t *testing.T) {
	pt := NewPausableTicker(5 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pt.Run(ctx)

	// Потребитель тиков: горутина тикера не должна застрять на отправке.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-pt.C():
			case <-stop:
				return
			}
		}
	}()

	const (
		workers    = 8
		iterations = 200
	)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				switch id % 3 {
				case 0:
					pt.Pause()
				case 1:
					pt.Resume()
				default:
					_ = pt.State()
				}
			}
		}(i)
	}

	wg.Wait()
	close(stop)

	switch st := pt.State(); st {
	case Idle, Running, Paused, Stopped:
	default:
		t.Fatalf("недопустимое состояние тикера: %v", st)
	}
}
