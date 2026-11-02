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

type PausableTicker struct {
	interval time.Duration

	mu      sync.Mutex
	state   State
	ticker  *time.Ticker
	started bool
	stopped bool

	tickChan chan time.Time
	cmdCh    chan struct{}
	stopCh   chan struct{}
	doneCh   chan struct{}
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

func (pt *PausableTicker) Run(ctx context.Context) {
	pt.mu.Lock()
	if pt.started || pt.state == Stopped {
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
		close(pt.tickChan)
		close(pt.doneCh)
	}()

	active := true
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
				continue
			}
			if !pt.deliver(ctx, t) {
				return
			}
		}
	}
}

func (pt *PausableTicker) applyState(active bool, ticker *time.Ticker) bool {
	switch pt.State() {
	case Running:
		if !active {
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

func (pt *PausableTicker) State() State {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.state
}

func (pt *PausableTicker) isRunning() bool {
	return pt.State() == Running
}

func (pt *PausableTicker) C() <-chan time.Time {
	return pt.tickChan
}

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

func (pt *PausableTicker) Stop() {
	pt.mu.Lock()
	if !pt.stopped {
		pt.stopped = true
		pt.state = Stopped
		close(pt.stopCh)
		if !pt.started {
			close(pt.tickChan)
			close(pt.doneCh)
		}
	}
	pt.mu.Unlock()

	<-pt.doneCh
}

func (pt *PausableTicker) signal() {
	select {
	case pt.cmdCh <- struct{}{}:
	default:
	}
}
