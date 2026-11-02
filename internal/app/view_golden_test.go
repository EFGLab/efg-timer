package app

import (
	"testing"
	"time"

	"github.com/EFGLab/efg-timer/internal/state"
	"github.com/EFGLab/efg-timer/internal/timer"
	"github.com/go-gui-org/go-gui/gui"
)

// Golden-тест внешнего вида.
//
// Снимок фиксирует то, что видит владелец: цвета, отступы, размеры и
// геометрию виджетов. Любая правка стилей ломает снимок диффом — это и
// есть гейт.
//
// Перезапись снимков:
//
//	GOGUI_UPDATE_GOLDEN=1 go test ./internal/app/ -run TestGolden -count=1
//
// Снимки лежат в internal/app/testdata/ (root.dark.golden,
// root.light.golden). Каталог testdata/failures/ с фактическим выводом
// при несовпадении генерируется библиотекой и в репозиторий не попадает.
//
// Детерминизм: состояние окна в тесте фиксированное (остаток таймера —
// ровно минута, состояние — Idle, сервер выключен, ошибок нет), часы в
// golden-кадре закреплены библиотекой, а таймер не запущен — тиков нет,
// счётчик не «мигает».

// goldenTheme — тема с крупным счётчиком: тот же форк, что приложение
// делает в New.
//
// Снимок должен показывать реальный вид приложения, а не вид библиотеки
// по умолчанию: без форка в снимке был бы мелкий счётчик, каким
// приложение никогда не бывает.
func goldenTheme(base gui.Theme) gui.Theme {
	return appTheme(base)
}

// newGoldenWindow — окно для снимка: то же состояние, что в приложении
// (onAppInit), но без запущенного таймера — тики сделали бы снимок
// «мигающим».
func newGoldenWindow(t *testing.T) (*gui.Window, *state.AppState) {
	t.Helper()

	st := state.InitialAppState()
	st.Timer = timer.New(time.Minute)

	st.RootState.Remaining = st.Timer.Remaining()
	st.RootState.TimerState = st.Timer.State()

	w := gui.NewTestWindow(gateCleanuper{t: t}, gui.WindowCfg{
		AppInfo: info,
		State:   st,
		Width:   800,
		Height:  600,
	})

	return w, st
}

// TestGoldenRoot — снимок корневого экрана в обеих темах.
//
// Приложение всегда тёмное (app.New ставит форк тёмной темы), но
// библиотека записывает снимок в обеих темах: тёмный снимок — реальный
// вид приложения, светлый — тот же макет в других цветах.
//
// Почему только корневой экран: в диалоге настроек иконки сайдбара —
// SVG (assets/*.svg), а в тестовом окне SVG-парсера нет («svg: no SVG
// parser configured»), поэтому в снимке диалога иконки были бы пустыми.
// Такой снимок фиксировал бы не вид, а отсутствие парсера.
//
// Почему снимок не обёрнут в таймаут (в отличие от гейта): TestGolden
// рисует кадры без фоновой работы — таймер не запущен, окно не
// инвалидируется извне, — поэтому зависать там нечему.
func TestGoldenRoot(t *testing.T) {
	// TestGolden снимает темы из пакета go-gui, поэтому подменяем их
	// форками приложения на время прогона и возвращаем назад: темы —
	// общепроцессное состояние, а не состояние окна.
	origDark, origLight := gui.ThemeDark, gui.ThemeLight
	t.Cleanup(func() {
		gui.ThemeDark, gui.ThemeLight = origDark, origLight
	})

	gui.ThemeDark = goldenTheme(origDark)
	gui.ThemeLight = goldenTheme(origLight)

	w, _ := newGoldenWindow(t)

	w.TestRender(rootView)
	w.TestGolden(t, gui.GoldenCfg{Name: "root"})
}
