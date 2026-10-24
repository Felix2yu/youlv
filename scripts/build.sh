#!/usr/bin/env bash
# 游履构建脚本 —— 前端产物内嵌进 Go 二进制，产出自包含单文件。
#
# 产物：bin/youlv（前端已内嵌；运行时只需要它 + 一个 SQLite 数据文件）
# 前置：Go + Node（大版本见 .nvmrc）+ pnpm
#
# 用法：
#   ./scripts/build.sh
#   GO_BIN=/path/to/go NODE_BIN=/path/to/node ./scripts/build.sh
#
# 约定见 Felix2yu/.github/docs/build-script.md
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

step() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
die() { printf '\033[1;31m错误：\033[0m %s\n' "$1" >&2; exit 1; }

# ---------- Node（大版本从 .nvmrc 读，脚本里不写死版本号）----------
# 升级 Node 只改 .nvmrc / web/package.json 的 engines / Dockerfile，不必回头动脚本。
node_major() { "$1" -v 2>/dev/null | sed 's/^v\([0-9]*\).*/\1/'; }

REQ_MAJOR="$(sed -n 's/^v\{0,1\}\([0-9][0-9]*\).*/\1/p' "$ROOT/.nvmrc" | head -1)"
[ -n "$REQ_MAJOR" ] || die "读不出 $ROOT/.nvmrc 里的 Node 大版本"

# 候选依次为：PATH → nvm → Homebrew / usr-local（含未带版本号的 node，即当前默认大版本）。
# 只看大版本，不锁小版本，避免升个补丁就要改脚本。
node_candidates() {
  command -v node 2>/dev/null || true
  ls -1d "${NVM_DIR:-$HOME/.nvm}"/versions/node/v*/bin/node 2>/dev/null | sort -Vr || true
  echo "/opt/homebrew/opt/node@${REQ_MAJOR}/bin/node"
  echo "/opt/homebrew/opt/node/bin/node"
  echo "/usr/local/opt/node@${REQ_MAJOR}/bin/node"
  echo "/usr/local/opt/node/bin/node"
}

# want=exact 要求大版本恰好相同；want=newer 接受更高的大版本（本机没装要求的大版本时的退路）。
pick_node() {
  local want="$1" cand maj
  while IFS= read -r cand; do
    [ -n "$cand" ] && [ -x "$cand" ] || continue
    maj="$(node_major "$cand")"
    [ -n "$maj" ] || continue
    if [ "$want" = exact ]; then
      [ "$maj" = "$REQ_MAJOR" ] && { printf '%s\n' "$cand"; return 0; }
    else
      [ "$maj" -gt "$REQ_MAJOR" ] && { printf '%s\n' "$cand"; return 0; }
    fi
  done < <(node_candidates)
  return 1
}

if [ -z "${NODE_BIN:-}" ]; then
  NODE_BIN="$(pick_node exact || pick_node newer || true)"
fi

if [ -z "${NODE_BIN:-}" ] || [ ! -x "${NODE_BIN:-}" ]; then
  die "找不到 Node ${REQ_MAJOR}（当前 PATH 上是 $(node -v 2>/dev/null || echo 无)）。
     请安装：nvm install ${REQ_MAJOR}，或显式指定：NODE_BIN=/path/to/node $0"
fi

ACTUAL_MAJOR="$(node_major "$NODE_BIN")"
[ -n "$ACTUAL_MAJOR" ] || die "无法识别 $NODE_BIN 的版本"

# 硬校验：低于 .nvmrc 要求的大版本直接拒绝构建。有了这一条，「CI 里构建成功」
# 本身就证明 CI 用的是够新的 Node，不必去翻日志。显式传 NODE_BIN 时同样校验。
[ "$ACTUAL_MAJOR" -ge "$REQ_MAJOR" ] || \
  die "Node 大版本过低：.nvmrc 要求 ${REQ_MAJOR}，$NODE_BIN 是 ${ACTUAL_MAJOR}"

# 让 pnpm 与 NODE_BIN 同源，避免「node 来自一处、包管理器来自另一处」。
export PATH="$(dirname "$NODE_BIN"):$PATH"

# ---------- Go（版本要求见 go.mod 的 go 指令）----------
if [ -z "${GO_BIN:-}" ]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  elif [ -x /opt/homebrew/bin/go ]; then
    GO_BIN=/opt/homebrew/bin/go
  elif [ -x /usr/local/go/bin/go ]; then
    GO_BIN=/usr/local/go/bin/go
  else
    die "找不到 go，可显式指定：GO_BIN=/path/to/go $0"
  fi
fi
[ -x "$GO_BIN" ] || die "GO_BIN 不可执行：$GO_BIN"

# ---------- pnpm（版本由 web/package.json 的 packageManager 决定）----------
PNPM_BIN="${PNPM_BIN:-$(command -v pnpm || true)}"
[ -n "$PNPM_BIN" ] && [ -x "$PNPM_BIN" ] || \
  die "找不到 pnpm（安装：npm i -g pnpm；版本由 web/package.json 的 packageManager 决定）"

printf '  node %s（%s）\n' "$("$NODE_BIN" -v)" "$NODE_BIN"
printf '  go   %s（%s）\n' "$("$GO_BIN" version | awk '{print $3}')" "$GO_BIN"

# 「.nvmrc 写 26、本机只装了 28」这类情况：能跑，但要让用的人知道用的不是声明的大版本。
if [ "$ACTUAL_MAJOR" != "$REQ_MAJOR" ]; then
  printf '  ⚠ .nvmrc 要求 Node %s，实际用的是 %s（本机没有 %s 大版本，已退让到更高的版本）\n' \
    "$REQ_MAJOR" "$ACTUAL_MAJOR" "$REQ_MAJOR" >&2
fi

step "[1/3] 构建前端（vite）"
cd "$ROOT/web"
if [ ! -d node_modules ]; then
  printf '  安装前端依赖\n'
  "$PNPM_BIN" install --frozen-lockfile
fi
"$PNPM_BIN" run build

step "[2/3] 同步前端产物到 internal/web/dist"
cd "$ROOT"
rm -rf "$ROOT/internal/web/dist"
mkdir -p "$ROOT/internal/web/dist"
cp -R "$ROOT/web/dist/." "$ROOT/internal/web/dist/"
# tracked 的 .gitkeep 会被上面的 rm -rf 一起删掉，不补回来工作区就永久挂着一条删除记录。
touch "$ROOT/internal/web/dist/.gitkeep"

step "[3/3] 构建后端（go build）"
mkdir -p "$ROOT/bin"
"$GO_BIN" build -trimpath -ldflags "-s -w" -o "$ROOT/bin/youlv" ./cmd/youlv

printf '\n完成：%s\n' "$ROOT/bin/youlv"
printf '运行：cd %s && ./bin/youlv -addr :8802\n' "$ROOT"
