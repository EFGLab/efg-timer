package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/timer"
)

// Task e01s02-t3: страница виджета — EventSource('/events'), разбор события
// snapshot в MM:SS, прозрачный фон для наложения в OBS (e01s02-widget.md §5, §15).
// Веб-движок недоступен из Go-тестов — проверяем контракт страницы по фрагментам.

func TestWidgetPage(t *testing.T) {
	s := New(timer.New(time.Minute), 8089, nil)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/widget")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	html := string(body)

	for _, want := range []struct{ what, frag string }{
		{"инициализация EventSource", `new EventSource('/events')`},
		{"разбор именованного события snapshot", `addEventListener('snapshot'`},
		{"разбор payload", `remaining_ms`},
		{"элемент для MM:SS", `id="remaining"`},
		{"прозрачный фон для OBS", `background: transparent`},
	} {
		if !strings.Contains(html, want.frag) {
			t.Errorf("в /widget нет %s: фрагмент %q отсутствует", want.what, want.frag)
		}
	}
}
