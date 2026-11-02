#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

sudo chown -R "$(id -u):$(id -g)" /go /go/bin 2>/dev/null || true
sudo chown -R "$(id -u):$(id -g)" "$HOME/.cache/go-build" 2>/dev/null || true

sudo apt-get update -qq
sudo apt-get install -y -qq \
  fonts-dejavu-core fontconfig \
  libharfbuzz0b libglx0 libgl1 libegl1 libopengl0 \
  libxkbcommon0 libxkbcommon-x11-0 libdbus-1-3 \
  libxrender1 libxi6 libxrandr2 libxext6 libsm6 libice6

bash "$REPO_ROOT/scripts/setup.sh"
