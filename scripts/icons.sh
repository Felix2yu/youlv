#!/usr/bin/env bash
# 从 web/icon-src/ 的母版 SVG 生成全套图标资产到 web/public/。
#
# 母版分工：
#   icon-rounded.svg  圆角版 —— 浏览器标签页（favicon.svg / favicon.ico）
#   icon-square.svg   满幅方版 —— PWA 图标与 iOS 主屏（系统自己裁角）
#   icon-maskable.svg 安全区版 —— Android adaptive icon（图形居中在安全圆内）
#   mask-icon.svg     单色剪影 —— Safari 固定标签页
#
# 依赖：macOS qlmanage（WebKit 渲染 SVG，质量远好于 ImageMagick 内置渲染器）
#      + ImageMagick magick（缩放与合成 ico）。
# 用法：./scripts/icons.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/web/icon-src"
OUT="$ROOT/web/public"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

command -v qlmanage >/dev/null 2>&1 || { echo "需要 qlmanage（macOS 自带）" >&2; exit 1; }
command -v magick >/dev/null 2>&1 || { echo "需要 ImageMagick（brew install imagemagick）" >&2; exit 1; }

step() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }

step "渲染母版 SVG（qlmanage，1024px）"
qlmanage -t -s 1024 -o "$TMP" \
  "$SRC/icon-rounded.svg" "$SRC/icon-square.svg" "$SRC/icon-maskable.svg" >/dev/null
png() { printf '%s/%s.svg.png' "$TMP" "$1"; }

step "favicon（圆角版）"
cp "$SRC/icon-rounded.svg" "$OUT/favicon.svg"
cp "$SRC/mask-icon.svg" "$OUT/mask-icon.svg"
magick "$(png icon-rounded)" -resize 48x48 "$TMP/r-48.png"
magick "$(png icon-rounded)" -resize 32x32 "$TMP/r-32.png"
magick "$(png icon-rounded)" -resize 16x16 "$TMP/r-16.png"
magick "$TMP/r-48.png" "$TMP/r-32.png" "$TMP/r-16.png" "$OUT/favicon.ico"

step "PWA / apple-touch（满幅方版）"
magick "$(png icon-square)" -resize 512x512 "$OUT/icon-512.png"
magick "$(png icon-square)" -resize 192x192 "$OUT/icon-192.png"
magick "$(png icon-square)" -resize 180x180 "$OUT/apple-touch-icon.png"

step "maskable（Android 自适应安全区版）"
magick "$(png icon-maskable)" -resize 512x512 "$OUT/icon-maskable-512.png"
magick "$(png icon-maskable)" -resize 192x192 "$OUT/icon-maskable-192.png"

step "完成：$(ls "$OUT" | tr '\n' ' ')"
