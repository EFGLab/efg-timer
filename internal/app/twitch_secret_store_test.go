package app

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"
)

// TestGUISecretStore — адаптер поверх go-gui: отсутствующий секрет это
// found=false без ошибки, остальное проксируется как есть. TestWindow
// использует memory store, реальное хранилище ОС не трогаем.
func TestGUISecretStore(t *testing.T) {
	w := gui.NewTestWindow(t, gui.WindowCfg{AppInfo: info})
	store := guiSecretStore{w: w}

	if _, found, err := store.Load("twitch.session"); err != nil || found {
		t.Fatalf("Load пустого ключа: found=%v err=%v, want false, nil", found, err)
	}
	if err := store.Save("twitch.session", []byte("value")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, found, err := store.Load("twitch.session")
	if err != nil || !found || string(got) != "value" {
		t.Fatalf("Load: found=%v value=%q err=%v", found, got, err)
	}
	if err := store.Delete("twitch.session"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, found, err := store.Load("twitch.session"); err != nil || found {
		t.Fatalf("Load после Delete: found=%v err=%v, want false, nil", found, err)
	}
}
