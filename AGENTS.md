# AGENTS.md

给 AI 代理与本仓库协作者的项目说明。

## 项目

「游履」（youlv）：自托管的生活足迹记录应用。Go + SQLite 后端（标准库 `net/http`，**不引入 chi/gin**）、React 19 + Vite 8 + Tailwind v4 前端，前端产物经 `go:embed` 内嵌为单二进制。

## 目录结构

```
cmd/youlv/         # main：flag 解析、装配、优雅关闭；backup 子命令在 main 最前面拦截
internal/
  api/             # ServeMux 路由与 handler，写侧全在 importer
  store/           # 读侧查询（时间轴/地点/统计/地图/天气）
  importer/        # 导入管线：预检 → 暂存（NDJSON）→ 单事务提交 → 整批回滚
  csvin/           # CSV 解码、类型识别、字段映射、时间/数值解析
  geo/             # GCJ-02 ↔ WGS-84 变换、Haversine、量化
  db/              # 打开/迁移（migrations/*.sql）
  config/          # YOULV_* 环境变量解析
  web/             # go:embed all:dist + SPA 静态托管
web/               # 前端工程（pnpm；packageManager 声明版本）
scripts/build.sh   # 唯一构建实现（前端 → 同步 embed 位 → go build）
```

## 开发命令

```bash
./scripts/build.sh        # 全量构建 → bin/youlv
make dev                  # go run ./cmd/youlv（默认 :8802）
make test                 # go test ./...
make lint                 # go vet + cd web && pnpm run typecheck
make fmt                  # gofmt
cd web && pnpm run lint   # prettier --check + eslint
```

## 约束

- **包管理器一律 pnpm**，版本由 `web/package.json` 的 `packageManager` 唯一决定（当前 11.22.0）。不要用 npm/yarn/bun，不要生成第二种锁文件。
- Node 大版本由根目录 `.nvmrc` 声明（26），构建脚本会硬校验。
- Go 只用标准库 + `modernc.org/sqlite`（纯 Go，`CGO_ENABLED=0` 可编译）。不要引入 Web 框架。
- 时间处理：显式构造 `Asia/Shanghai`（FixedZone +8），不依赖宿主 TZ；业务表冗余存本地日期/月份/星期等列，查询层不做时区换算。
- 坐标：**WGS-84 是权威存储**（`places.lat/lon`、`visits.lat/lon`），GCJ-02 是渲染副本（`*_gcj`），原始导入值留在 `*_raw`。改坐标逻辑必须先读 `internal/geo` 的文档与黄金值测试。
- 幂等：导入按 SHA-256 去重键判定，重复预检必须显示 ready=0；改导入逻辑后跑 `TestSecondPreviewReportsZeroReady` 与（有真实数据时）`YOULV_REAL_CSV_DIR=... go test -run TestRealFullImport`。
- 迁移只增不改：新表/新列写新的 `migrations/NNN_*.sql`，禁止修改已应用的迁移文件。
- Docker：基镜 `ghcr.io/felix2yu/base:alpine-3.24`，PUID/PGID 降权已下沉基镜，本仓不维护 entrypoint；`.dockerignore` 不得排除 `bin/`（Dockerfile COPY 源）。

## 数据约定

数据目录（默认 `./data`）挂载为容器 `/app/data`：`youlv.db`（SQLite，WAL）+ `tmp/imports/`（预检暂存，7 天自动清理）。备份契约：`youlv backup` 输出一致性快照到 stdout。
