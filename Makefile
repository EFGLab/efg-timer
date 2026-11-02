GO ?= go
TOOLS_DIR := .tools
BIN_DIR := $(TOOLS_DIR)/bin
VERSION ?= dev
MODULE := github.com/EFGLab/efg-timer

# Под Windows GNU Make зовёт cmd.exe, поэтому POSIX-рецепты (mkdir -p, rm -rf)
# не работают — для каждой ОС свои команды.
ifeq ($(OS),Windows_NT)
EXE := .exe
MKDIR_BUILD = cmd /c if not exist build mkdir build
RM_BUILD = cmd /c if exist build rmdir /s /q build
RUN_BIN = build\timer-widget.exe
else
EXE :=
MKDIR_BUILD = mkdir -p build
RM_BUILD = rm -rf build
RUN_BIN = ./build/timer-widget
endif

TIMER_WIDGET := build/timer-widget$(EXE)

.PHONY: build test lint run release clean

build:
	$(MKDIR_BUILD)
	$(GO) build -o $(TIMER_WIDGET) ./cmd/timer-widget

test:
	$(GO) test ./...

lint:
	$(GO) vet ./...

release: build
ifeq ($(OS),Windows_NT)
	@echo "release: цель только для POSIX — собирает linux-тарболл"
	@exit 1
else
	mkdir -p build/release/timer-widget-$(VERSION)
	$(GO) build -trimpath -ldflags "-s -w -X $(MODULE)/internal/app.Version=$(VERSION)" \
		-o build/release/timer-widget-$(VERSION)/timer-widget ./cmd/timer-widget
	cp internal/app/assets/icon.png build/release/timer-widget-$(VERSION)/
	cp scripts/timer-widget.desktop build/release/timer-widget-$(VERSION)/
	cp scripts/install.sh build/release/timer-widget-$(VERSION)/
	tar -czf build/release/timer-widget-$(VERSION)-linux-amd64.tar.gz -C build/release timer-widget-$(VERSION)
	rm -rf build/release/timer-widget-$(VERSION)
	@echo "release: build/release/timer-widget-$(VERSION)-linux-amd64.tar.gz"
	@echo "install after unpacking: ./install.sh"
endif

run: build
	$(RUN_BIN)

clean:
	$(RM_BUILD)
