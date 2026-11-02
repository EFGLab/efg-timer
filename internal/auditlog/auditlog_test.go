package auditlog

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/timer"
)

// testEntry — запись журнала с различимыми полями.
func testEntry(n int) timer.AuditEntry {
	return timer.AuditEntry{
		At:     time.Date(2026, 10, 19, 12, 0, n, 0, time.UTC),
		Op:     timer.OpAdd,
		Source: timer.SourceDonatex,
		Detail: "донат №" + string(rune('0'+n)),
		Before: time.Duration(n) * time.Second,
		After:  time.Duration(n+1) * time.Second,
	}
}

func TestPath(t *testing.T) {
	appID := "ru.egorfromgor.efg-timer.test"
	t.Cleanup(func() {
		if dir, err := os.UserConfigDir(); err == nil {
			_ = os.RemoveAll(filepath.Join(dir, appID))
		}
	})

	path, err := Path(appID)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}

	cfgDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("os.UserConfigDir: %v", err)
	}
	if dir := filepath.Dir(path); dir != filepath.Join(cfgDir, appID) {
		t.Fatalf("путь %q вне папки %q", path, filepath.Join(cfgDir, appID))
	}
	if !strings.Contains(path, appID) {
		t.Fatalf("путь %q не содержит app id %q", path, appID)
	}
}

func TestAppendLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	store := NewStore(path)

	want := []timer.AuditEntry{testEntry(1), testEntry(2)}
	for _, e := range want {
		if err := store.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	// JSONL: одна запись — одна строка.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("строк в файле %d, ждали %d", len(lines), len(want))
	}

	got, err := store.Load(10)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("записей %d, ждали %d", len(got), len(want))
	}
	for i := range want {
		if !got[i].At.Equal(want[i].At) || got[i].Op != want[i].Op ||
			got[i].Source != want[i].Source || got[i].Detail != want[i].Detail ||
			got[i].Before != want[i].Before || got[i].After != want[i].After {
			t.Fatalf("запись %d не совпала: %+v", i, got[i])
		}
	}
}

func TestLoadLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	store := NewStore(path)

	var want []timer.AuditEntry
	for i := 1; i <= 5; i++ {
		e := testEntry(i)
		want = append(want, e)
		if err := store.Append(e); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got, err := store.Load(2)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("записей %d, ждали 2", len(got))
	}
	if !got[0].At.Equal(want[3].At) || !got[1].At.Equal(want[4].At) {
		t.Fatalf("Load вернул не последние записи: %+v", got)
	}
}

func TestLoadSkipsBrokenLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	store := NewStore(path)

	if err := store.Append(testEntry(1)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	if _, err := f.WriteString("это не json\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	f.Close()

	if err := store.Append(testEntry(2)); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got, err := store.Load(10)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("записей %d, ждали 2", len(got))
	}
	if !got[0].At.Equal(testEntry(1).At) || !got[1].At.Equal(testEntry(2).At) {
		t.Fatalf("Load вернул не те записи: %+v", got)
	}
}

func TestLoadMissingFile(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "нет-файла.log"))

	got, err := store.Load(10)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("записей %d, ждали 0", len(got))
	}
}

func TestAppendConcurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	store := NewStore(path)

	const writers = 8
	const each = 25

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := store.Append(testEntry(i)); err != nil {
					t.Errorf("Append: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	got, err := store.Load(writers * each)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != writers*each {
		t.Fatalf("записей %d, ждали %d", len(got), writers*each)
	}
}
