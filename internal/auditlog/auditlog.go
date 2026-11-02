package auditlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/EFGLab/efg-timer/internal/timer"
)

const fileName = "audit.log"

func Path(appID string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("auditlog: папка настроек: %w", err)
	}
	if dir == "" {
		return "", errors.New("auditlog: пустая папка настроек")
	}

	appDir := filepath.Join(dir, appID)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return "", fmt.Errorf("auditlog: создание папки %s: %w", appDir, err)
	}

	return filepath.Join(appDir, fileName), nil
}

type entry struct {
	At     time.Time     `json:"at"`
	Op     timer.AuditOp `json:"op"`
	Source timer.Source  `json:"source"`
	Detail string        `json:"detail,omitempty"`
	Before time.Duration `json:"before"`
	After  time.Duration `json:"after"`
}

type Store struct {
	path string

	mu sync.Mutex
}

func NewStore(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Load(limit int) ([]timer.AuditEntry, error) {
	if limit <= 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("auditlog: открытие %s: %w", s.path, err)
	}
	defer f.Close()

	entries := make([]timer.AuditEntry, 0, limit)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}

		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}

		entries = append(entries, e.toAudit())
		if len(entries) > limit {
			entries = entries[len(entries)-limit:]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("auditlog: чтение %s: %w", s.path, err)
	}

	return entries, nil
}

func (s *Store) Append(e timer.AuditEntry) error {
	line, err := json.Marshal(entryOf(e))
	if err != nil {
		return fmt.Errorf("auditlog: кодирование записи: %w", err)
	}
	line = append(line, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("auditlog: создание папки %s: %w", dir, err)
		}
	}

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("auditlog: открытие %s: %w", s.path, err)
	}
	defer f.Close()

	if _, err := f.Write(line); err != nil {
		return fmt.Errorf("auditlog: запись в %s: %w", s.path, err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("auditlog: синхронизация %s: %w", s.path, err)
	}

	return nil
}

func entryOf(e timer.AuditEntry) entry {
	return entry{
		At:     e.At,
		Op:     e.Op,
		Source: e.Source,
		Detail: e.Detail,
		Before: e.Before,
		After:  e.After,
	}
}

func (e entry) toAudit() timer.AuditEntry {
	return timer.AuditEntry{
		At:     e.At,
		Op:     e.Op,
		Source: e.Source,
		Detail: e.Detail,
		Before: e.Before,
		After:  e.After,
	}
}
