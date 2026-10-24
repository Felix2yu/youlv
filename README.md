# 游履

记录每天到过哪里。自托管的生活足迹应用：把 Rond 导出的 CSV 攒成一份可检索、可回看、可统计的个人足迹档案。

> **数据声明**：本仓库 `testdata/rond-export/` 下的 CSV 是**脱敏合成样本**——地点名、地址、活动、标签均已替换为合成值，坐标经统一平移且保留了原始的坐标系不变式（地点表 = GCJ-02、到访表 = WGS-84），时间整体平移 728 天。仓库内**不含任何真实个人轨迹数据**；你自己的数据请用 `youlv backup` 自行保管。

仿照 iOS 应用 [Rond](https://apps.apple.com/cn/app/rond/id1669903815)（生活足迹 · 旅行日记 · 时间轴）的数据模型与功能设计。

## 功能

- **一键导入 Rond 导出**：地点记录、到访记录、交通记录、天气数据四份 CSV 一起拖进来即可。自动识别文件类型、字段映射、逐行校验；重复数据自动跳过（可反复重导，不会写重）；天气冲突逐组裁决；整批可一键撤销。
- **坐标系自动纠正**：Rond 导出的地点表是 GCJ-02（火星坐标）、到访表是 WGS-84，导入时统一换算到 WGS-84 权威存储，并保留 GCJ-02 副本与原始值供回溯。地图可按两种坐标框架渲染。
- **时间轴**：按天回看到访明细（时段、时长、活动分类、标签、天气），月历热力图总览一个月的出行密度。
- **足迹地图**：默认纯自绘渲染（不请求任何外部瓦片），也可切换到在线底图（Leaflet + CARTO）。支持缩放、拖拽、按视野范围取点。
- **地点库**：全部到过的地方，按到访次数、最近到访排序；只在别处被提及的地点自动建「仅引用」桩，不混进足迹地图。
- **统计**：时间都花在哪（活动分布）、城市护照、通勤方式、什么时候在外面（星期 × 小时热力图）。

## 快速开始

```bash
# 本地构建（需要 Go、Node 26、pnpm）
./scripts/build.sh
./bin/youlv -addr :8802
# 浏览器打开 http://127.0.0.1:8802
```

### Docker

```bash
docker compose pull && docker compose up -d
```

数据存放在具名卷 `youlv-data`（容器内 `/app/data/youlv.db`，单个 SQLite 文件）。

## 配置

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `YOULV_ADDR` | `:8802` | 监听地址（也可用 `-addr` 参数） |
| `YOULV_DATA_DIR` | `./data` | 数据目录（数据库与导入暂存都在这里） |
| `YOULV_DB` | `<data>/youlv.db` | SQLite 文件路径 |
| `YOULV_TZ` | `Asia/Shanghai` | 时区，决定「今天」的归属与时刻的本地化展示 |
| `YOULV_WEB_DIR` | （空） | 开发时外挂前端产物目录（默认用内嵌产物） |

## 备份

```bash
youlv backup > youlv-备份.db
```

只读打开数据库，以 `VACUUM INTO` 生成一致性快照写到 stdout，日志走 stderr，不锁库、不影响在线服务。数据文件不存在时报错退出，绝不产出空备份。容器内为 `docker exec youlv /app/youlv backup`。

## 开发

```bash
make dev      # 起 Go 服务（:8802）
cd web && pnpm run dev   # 另开终端，前端热更新（:5173，/api 代理到 8802）
make test     # Go 测试
make lint     # go vet + 前端 typecheck
```

目录结构见 [AGENTS.md](AGENTS.md)。

## License

MIT
