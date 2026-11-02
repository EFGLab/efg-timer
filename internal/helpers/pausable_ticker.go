package helpers

import (
	"context"
	"sync"
	"time"
)

type State int

const (
	Idle State = iota
	Stopped
	Running
	Paused
)

// PausableTicker — тикер с поддержкой паузы.
//
// Состояние тикера защищено мьютексом (пишет горутина тикера, читают
// UI-потоки). Run идемпотентен: повторный (в том числе одновременный)
// вызов не поднимает второй тикер. Pause/Resume не блокируют вызывающий
// поток.
//
// Архитектура команд: вызывающий не пишет в канал команды напрямую, а меняет
// желаемое состояние (pt.state) под мьютексом и кладёт «пустой» сигнал в
// буферизованный cmdCh (неблокирующая отправка). Горутина тикера читает
// фактическое состояние и синхронизирует с ним свой time.Ticker. Так команды
// не блокируют вызывающий (в том числе вызов из самого цикла чтения тиков) и
// не теряются: горутина всегда смотрит на актуальное состояние, а не на
// «момент сигнала».
type PausableTicker struct {
	interval time.Duration

	mu      sync.Mutex
	state   State
	ticker  *time.Ticker
	started bool // горутина тикера уже запущена
	stopped bool // остановка уже выполнена (stopCh закрыт)

	tickChan chan time.Time
	cmdCh    chan struct{} // сигнал «состояние изменилось»
	stopCh   chan struct{} // остановка (закрывается — рассылка всем ожидающим)
	doneCh   chan struct{} // горутина тикера завершилась
}

func NewPausableTicker(d time.Duration) *PausableTicker {
	pt := &PausableTicker{
		interval: d,
		state:    Idle,
		tickChan: make(chan time.Time),
		cmdCh:    make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	return pt
}

// Run запускает горутину тикера. Повторный вызов (в том числе одновременный
// с первым) безопасен: второй тикер не поднимается. После остановки тикер
// не перезапускается: состояние остаётся Stopped.
func (pt *PausableTicker) Run(ctx context.Context) {
	pt.mu.Lock()
	if pt.started || pt.state == Stopped {
		// Тикер уже запущен или остановлен — выходим, ничего не меняя.
		pt.mu.Unlock()
		return
	}
	pt.started = true
	pt.state = Running
	ticker := time.NewTicker(pt.interval)
	pt.ticker = ticker
	pt.mu.Unlock()

	defer func() {
		ticker.Stop()
		pt.mu.Lock()
		pt.ticker = nil
		pt.state = Stopped
		pt.mu.Unlock()
		// Канал тиков закрываем, чтобы потребитель (for range C()) не висел
		// после остановки тикера.
		close(pt.tickChan)
		close(pt.doneCh)
	}()

	active := true // time.Ticker сейчас тикает
	for {
		select {
		case <-ctx.Done():
			return
		case <-pt.stopCh:
			return
		case <-pt.cmdCh:
			active = pt.applyState(active, ticker)
		case t := <-ticker.C:
			if !pt.isRunning() {
				// Тик пришёл в паузе — выбрасываем его.
				continue
			}
			if !pt.deliver(ctx, t) {
				return
			}
		}
	}
}

// applyState синхронизирует time.Ticker с желаемым состоянием и возвращает
// признак того, тикает ли time.Ticker сейчас. Вызывается только из горутины
// тикера.
func (pt *PausableTicker) applyState(active bool, ticker *time.Ticker) bool {
	switch pt.State() {
	case Running:
		if !active {
			// Возобновление: первый тик — через полный интервал.
			ticker.Reset(pt.interval)
			return true
		}
	case Paused:
		if active {
			ticker.Stop()
			return false
		}
	}
	return active
}

// deliver передаёт тик потребителю. Прерывается остановкой или отменой
// контекста, иначе Stop() ждал бы завершения горутины бесконечно.
func (pt *PausableTicker) deliver(ctx context.Context, t time.Time) bool {
	select {
	case pt.tickChan <- t:
		return true
	case <-pt.stopCh:
		return false
	case <-ctx.Done():
		return false
	}
}

// State возвращает состояние тикера. Потокобезопасно.
func (pt *PausableTicker) State() State {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.state
}

// isRunning — быстрая проверка «тикер должен тикать».
func (pt *PausableTicker) isRunning() bool {
	return pt.State() == Running
}

func (pt *PausableTicker) C() <-chan time.Time {
	return pt.tickChan
}

// Pause переводит тикер в паузу. Не блокирует вызывающий поток.
// Повторный вызов безопасен: состояние не меняется.
func (pt *PausableTicker) Pause() {
	pt.mu.Lock()
	if pt.state != Running {
		pt.mu.Unlock()
		return
	}
	pt.state = Paused
	pt.mu.Unlock()

	pt.signal()
}

// Resume возвращает тикер в работу. Не блокирует вызывающий поток.
// Повторный вызов безопасен: состояние не меняется.
func (pt *PausableTicker) Resume() {
	pt.mu.Lock()
	if pt.state != Paused {
		pt.mu.Unlock()
		return
	}
	pt.state = Running
	pt.mu.Unlock()

	pt.signal()
}

// Stop останавливает тикер и дожидается завершения его горутины.
func (pt *PausableTicker) Stop() {
	pt.mu.Lock()
	if !pt.stopped {
		pt.stopped = true
		pt.state = Stopped
		close(pt.stopCh)
		if !pt.started {
			// Горутина тикера не запускалась — завершаем её вручную,
			// иначе Stop() ждал бы Done вечно.
			close(pt.tickChan)
			close(pt.doneCh)
		}
	}
	pt.mu.Unlock()

	<-pt.doneCh // ждём завершения горутины тикера
}

// signal будит горутину тикера: состояние уже изменено под мьютексом, осталось
// синхронизировать сам time.Ticker. Отправка неблокирующая — «лишний» сигнал
// не нужен: горутина всё равно читает актуальное состояние.
func (pt *PausableTicker) signal() {
	select {
	case pt.cmdCh <- struct{}{}:
	default:
	}
}
