-- 游履初始 schema。
--
-- 三条贯穿全库的设计约定：
--
-- 1. 时间一律存 INTEGER unix 秒（UTC）。冗余的 local_date / local_month /
--    local_year / local_hour / local_dow 由应用层在同一个时区下算好写入，
--    不用 SQLite 的 strftime(..., 'localtime') —— 后者依赖宿主机 TZ 环境
--    变量，换台机器结果就变了。
--
-- 2. 坐标以 WGS-84 为权威（lat/lon），同时保留 GCJ-02 副本（lat_gcj/lon_gcj）
--    供国内底图直接渲染，前端不做浮点变换。visits 另存 lat_raw/lon_raw 作为
--    CSV 原始值，使任何导入算法错误都可事后审计修复。
--
-- 3. 每张业务表都有 import_batch_id，使「按批次回滚」成为机械操作。

CREATE TABLE IF NOT EXISTS schema_version (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- ---------- 设置 ----------
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- ---------- 活动字典 ----------
-- 17 种中文活动来自 Rond 导出，且 Rond 支持自定义，故用字典表而非枚举列。
CREATE TABLE IF NOT EXISTS activities (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    color      TEXT NOT NULL DEFAULT '#8E8E93',
    icon       TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_system  INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);

-- ---------- 导入审计（先建，业务表要引用它）----------
CREATE TABLE IF NOT EXISTS imports (
    id            INTEGER PRIMARY KEY,
    batch_no      TEXT NOT NULL UNIQUE,
    status        TEXT NOT NULL,      -- preview|running|success|failed|rolled_back
    source        TEXT NOT NULL DEFAULT 'rond-csv',
    app_version   TEXT,
    options_json  TEXT NOT NULL DEFAULT '{}',
    stats_json    TEXT NOT NULL DEFAULT '{}',
    report_json   TEXT NOT NULL DEFAULT '{}',
    rows_total    INTEGER NOT NULL DEFAULT 0,
    rows_inserted INTEGER NOT NULL DEFAULT 0,
    rows_skipped  INTEGER NOT NULL DEFAULT 0,
    rows_failed   INTEGER NOT NULL DEFAULT 0,
    started_at    INTEGER,
    finished_at   INTEGER,
    duration_ms   INTEGER,
    undone_at     INTEGER,
    error_summary TEXT,
    created_at    INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_imports_status ON imports(status, created_at DESC);

CREATE TABLE IF NOT EXISTS imports_files (
    id            INTEGER PRIMARY KEY,
    import_id     INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL,      -- places|visits|trips|weather
    file_name     TEXT NOT NULL,
    file_size     INTEGER NOT NULL DEFAULT 0,
    sha256        TEXT NOT NULL DEFAULT '',
    header_json   TEXT NOT NULL DEFAULT '[]',
    mapping_json  TEXT NOT NULL DEFAULT '{}',
    rows_total    INTEGER NOT NULL DEFAULT 0,
    rows_inserted INTEGER NOT NULL DEFAULT 0,
    rows_skipped  INTEGER NOT NULL DEFAULT 0,
    rows_failed   INTEGER NOT NULL DEFAULT 0,
    staged_path   TEXT
);
CREATE INDEX IF NOT EXISTS idx_imports_files_import ON imports_files(import_id);

CREATE TABLE IF NOT EXISTS import_issues (
    id        INTEGER PRIMARY KEY,
    import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
    kind      TEXT NOT NULL,
    line      INTEGER NOT NULL,       -- CSV 物理行号，1-based，含表头
    row_no    INTEGER NOT NULL,
    col       TEXT,
    field     TEXT,
    value     TEXT,
    severity  TEXT NOT NULL,          -- error|warn|info
    code      TEXT NOT NULL,
    message   TEXT NOT NULL,
    hint      TEXT
);
CREATE INDEX IF NOT EXISTS idx_import_issues_import ON import_issues(import_id, severity);
CREATE INDEX IF NOT EXISTS idx_import_issues_code   ON import_issues(import_id, code);

-- ---------- 地点 ----------
CREATE TABLE IF NOT EXISTS places (
    id              INTEGER PRIMARY KEY,
    name            TEXT NOT NULL,
    display_name    TEXT NOT NULL,           -- 重名消歧后的展示名
    name_normalized TEXT NOT NULL DEFAULT '',
    source_key      TEXT NOT NULL UNIQUE,    -- sha1(name|lat6,lon6) 或 stub:<sha1(name)>

    lat             REAL,                    -- WGS-84（权威）
    lon             REAL,
    lat_gcj         REAL,                    -- GCJ-02（CSV 原值 / 底图）
    lon_gcj         REAL,
    coord_frame     TEXT NOT NULL DEFAULT 'unknown',
    coord_precision INTEGER NOT NULL DEFAULT 6,

    radius_m        REAL,
    rating          REAL,
    country         TEXT,
    admin_area      TEXT,
    sub_admin_area  TEXT,
    city            TEXT,
    district        TEXT,
    street          TEXT,
    house_number    TEXT,

    origin          TEXT NOT NULL DEFAULT 'imported', -- imported|reference_only|visit_derived|manual
    is_stub         INTEGER NOT NULL DEFAULT 0,

    visit_count     INTEGER NOT NULL DEFAULT 0,
    first_visit_at  INTEGER,
    last_visit_at   INTEGER,

    import_batch_id INTEGER REFERENCES imports(id) ON DELETE SET NULL,
    created_at      INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at      INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_places_name   ON places(name);
CREATE INDEX IF NOT EXISTS idx_places_norm   ON places(name_normalized);
CREATE INDEX IF NOT EXISTS idx_places_city   ON places(city);
CREATE INDEX IF NOT EXISTS idx_places_admin  ON places(admin_area);
CREATE INDEX IF NOT EXISTS idx_places_origin ON places(origin);
CREATE INDEX IF NOT EXISTS idx_places_bbox   ON places(lat, lon);
-- 桩地点（孤儿引用）按名字唯一，保证同一孤儿只建一次。
CREATE UNIQUE INDEX IF NOT EXISTS uq_places_stub_name ON places(name) WHERE is_stub = 1;

-- ---------- 到访 ----------
CREATE TABLE IF NOT EXISTS visits (
    id              INTEGER PRIMARY KEY,
    place_id        INTEGER NOT NULL DEFAULT 0,   -- 0 = 未解析哨兵
    place_name_raw  TEXT NOT NULL,
    activity_id     INTEGER REFERENCES activities(id) ON DELETE SET NULL,
    activity_raw    TEXT NOT NULL,

    arrived_at      INTEGER NOT NULL,
    departed_at     INTEGER,                      -- NULL = 未记录或时间倒挂被置空
    duration_s      INTEGER,
    tz              TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    arrived_local   TEXT NOT NULL,
    departed_local  TEXT,
    local_date      TEXT NOT NULL,
    local_month     TEXT NOT NULL,
    local_year      INTEGER NOT NULL,
    local_hour      INTEGER NOT NULL,
    local_dow       INTEGER NOT NULL,             -- 0 = 周日

    lat             REAL,
    lon             REAL,
    lat_gcj         REAL,
    lon_gcj         REAL,
    lat_raw         REAL,
    lon_raw         REAL,
    coord_frame     TEXT NOT NULL DEFAULT 'wgs84',

    resolve_method     TEXT NOT NULL DEFAULT 'exact',
    resolve_dist_m     REAL,
    resolve_confidence TEXT NOT NULL DEFAULT 'high',

    note            TEXT,
    tags_raw        TEXT,
    dedup_key       TEXT NOT NULL UNIQUE,
    import_batch_id INTEGER REFERENCES imports(id) ON DELETE SET NULL,
    created_at      INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_visits_place_time ON visits(place_id, arrived_at);
CREATE INDEX IF NOT EXISTS idx_visits_date       ON visits(local_date, arrived_at);
CREATE INDEX IF NOT EXISTS idx_visits_month      ON visits(local_month, arrived_at);
CREATE INDEX IF NOT EXISTS idx_visits_year       ON visits(local_year);
CREATE INDEX IF NOT EXISTS idx_visits_activity   ON visits(activity_id, arrived_at);
CREATE INDEX IF NOT EXISTS idx_visits_time       ON visits(arrived_at);
CREATE INDEX IF NOT EXISTS idx_visits_bbox       ON visits(lat, lon);
CREATE INDEX IF NOT EXISTS idx_visits_batch      ON visits(import_batch_id);

-- ---------- 交通 ----------
CREATE TABLE IF NOT EXISTS trips (
    id                   INTEGER PRIMARY KEY,
    mode                 TEXT NOT NULL,
    mode_raw             TEXT NOT NULL,
    from_at              INTEGER NOT NULL,
    to_at                INTEGER NOT NULL,
    from_local           TEXT NOT NULL,
    to_local             TEXT NOT NULL,
    local_date           TEXT NOT NULL,
    local_month          TEXT NOT NULL,
    tz                   TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    duration_s           INTEGER NOT NULL,
    duration_declared_s  INTEGER,
    from_place_id        INTEGER REFERENCES places(id),
    to_place_id          INTEGER REFERENCES places(id),
    from_name_raw        TEXT NOT NULL DEFAULT '',
    to_name_raw          TEXT NOT NULL DEFAULT '',
    from_place_origin    TEXT NOT NULL DEFAULT 'resolved', -- resolved|stub|empty
    to_place_origin      TEXT NOT NULL DEFAULT 'resolved',
    from_lat REAL, from_lon REAL, from_lat_gcj REAL, from_lon_gcj REAL,
    to_lat   REAL, to_lon   REAL, to_lat_gcj   REAL, to_lon_gcj   REAL,
    note           TEXT,
    dedup_key      TEXT NOT NULL UNIQUE,
    import_batch_id INTEGER REFERENCES imports(id) ON DELETE SET NULL,
    created_at     INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_trips_time  ON trips(from_at);
CREATE INDEX IF NOT EXISTS idx_trips_date  ON trips(local_date);
CREATE INDEX IF NOT EXISTS idx_trips_month ON trips(local_month);
CREATE INDEX IF NOT EXISTS idx_trips_mode  ON trips(mode, from_at);
CREATE INDEX IF NOT EXISTS idx_trips_from  ON trips(from_place_id);
CREATE INDEX IF NOT EXISTS idx_trips_to    ON trips(to_place_id);
CREATE INDEX IF NOT EXISTS idx_trips_batch ON trips(import_batch_id);

-- ---------- 天气 ----------
-- 独立表而非冗余到 visits：113 个到访地点没有天气数据，冗余后全是 NULL，
-- 而且会让 4280 行到访背上 15 列气象字段。
CREATE TABLE IF NOT EXISTS weather (
    id              INTEGER PRIMARY KEY,
    place_id        INTEGER NOT NULL REFERENCES places(id) ON DELETE CASCADE,
    place_name_raw  TEXT NOT NULL,
    hour_ts         INTEGER NOT NULL,      -- UTC 整点 unix 秒
    hour_local      TEXT NOT NULL,
    local_date      TEXT NOT NULL,
    local_month     TEXT NOT NULL,
    condition       TEXT,
    temp_c          REAL,
    feels_like_c    REAL,
    dew_point_c     REAL,
    humidity        REAL,                  -- 0..1
    precip_type     TEXT,
    precip_mm       REAL,
    precip_prob     REAL,
    uv_index        INTEGER,
    uv_level        TEXT,
    wind_dir        TEXT,
    wind_speed_ms   REAL,
    visibility_km   REAL,
    raw_json        TEXT,
    import_batch_id INTEGER REFERENCES imports(id) ON DELETE SET NULL,
    created_at      INTEGER NOT NULL DEFAULT (unixepoch()),
    updated_at      INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_weather_place_hour ON weather(place_id, hour_ts);
CREATE INDEX IF NOT EXISTS idx_weather_date  ON weather(local_date);
CREATE INDEX IF NOT EXISTS idx_weather_month ON weather(local_month);
CREATE INDEX IF NOT EXISTS idx_weather_batch ON weather(import_batch_id);

-- ---------- 标签 ----------
CREATE TABLE IF NOT EXISTS tags (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    color      TEXT,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE TABLE IF NOT EXISTS visit_tags (
    visit_id INTEGER NOT NULL REFERENCES visits(id) ON DELETE CASCADE,
    tag_id   INTEGER NOT NULL REFERENCES tags(id)   ON DELETE CASCADE,
    PRIMARY KEY (visit_id, tag_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_visit_tags_tag ON visit_tags(tag_id);
