#!/usr/bin/env bash
set -euo pipefail

prefix="${HOME}/.local"
bin_dir="${prefix}/bin"
desktop_dir="${prefix}/share/applications"
icon_dir="${prefix}/share/icons/hicolor/64x64/apps"

install -d "${bin_dir}" "${desktop_dir}" "${icon_dir}"
install -m 0755 timer-widget "${bin_dir}/timer-widget"
install -m 0644 timer-widget.desktop "${desktop_dir}/timer-widget.desktop"
install -m 0644 icon.png "${icon_dir}/timer-widget.png"

echo "TimerWidget установлен:"
echo "  бинарник: ${bin_dir}/timer-widget"
echo "  ярлык:    ${desktop_dir}/timer-widget.desktop"
echo "  иконка:   ${icon_dir}/timer-widget.png"
