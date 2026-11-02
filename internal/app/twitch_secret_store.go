package app

import (
	"errors"

	"github.com/EFGLab/efg-timer/internal/providers"
	"github.com/go-gui-org/go-gui/gui"
)

type guiSecretStore struct {
	w *gui.Window
}

func (s guiSecretStore) Load(key string) ([]byte, bool, error) {
	value, err := gui.LoadSecret(s.w, key)
	if errors.Is(err, gui.ErrSecretNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (s guiSecretStore) Save(key string, value []byte) error {
	return gui.SaveSecret(s.w, key, value)
}

func (s guiSecretStore) Delete(key string) error {
	return gui.DeleteSecret(s.w, key)
}

var _ providers.SecretStore = guiSecretStore{}
