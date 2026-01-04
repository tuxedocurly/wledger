#!/usr/bin/env bash
set -euo pipefail

out_dir="web/icons"
icons=(
  arrow-right-left
  box
  clipboard
  eye
  file
  github
  grip-horizontal
  import
  layout-grid
  lightbulb
  lock
  log-in
  log-out
  map-pin
  menu
  microchip
  minus
  moon
  pencil
  plus
  power
  refresh-ccw
  settings
  sun
  swatch-book
  trash
  triangle-alert
  users
  x
  zap
)

for icon in "${icons[@]}"; do
  go run github.com/dimmerz92/go-lucide-icons/cmd/golucide@latest templ "$icon" -out "$out_dir"
done
