#!/usr/bin/env bash
set -euo pipefail

echo "Installing Go tools..."
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest 2>/dev/null || true
go install golang.org/x/tools/cmd/goimports@latest 2>/dev/null || true
go install honnef.co/go/tools/cmd/staticcheck@latest 2>/dev/null || true
go install github.com/go-delve/delve/cmd/dlv@latest 2>/dev/null || true
