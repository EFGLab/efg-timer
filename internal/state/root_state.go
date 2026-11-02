package state

import (
	"time"

	"github.com/EFGLab/efg-timer/internal/helpers"
)

type ServerStatus struct {
	Up  bool
	URL string
	Err string
}

type RootState struct {
	Error        string
	ServerStatus ServerStatus

	TimeInput string

	// Remaining — последний известный остаток таймера. Значение приходит
	// из движка через gui.Stream и применяется на frame thread: вьюха
	// читает его отсюда, а не дёргает движок.
	Remaining time.Duration

	// TimerState — состояние таймера (Idle/Running/Paused) для кнопки
	// Start/Pause/Resume. Источник истины — движок.
	TimerState helpers.State
}

func initialRootState() *RootState {
	return &RootState{TimerState: helpers.Idle}
}
