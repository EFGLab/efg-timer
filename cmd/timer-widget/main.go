package main

import (
	"log/slog"

	"github.com/EFGLab/efg-timer/internal/app"

	_ "net/http/pprof"
)

func main() {
	slog.Info("run efg-timer", "version", app.Version)

	app.New()
}
