package db_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Felix2yu/youlv/internal/db"
)

// migrationCount 数一遍迁移文件。测试不该把版本号写死：每加一条迁移就要来改
// 测试，改的人多半会顺手把断言调成新数字，等于这条测试不再校验任何东西。
func migrationCount(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatalf("读迁移目录: %v", err)
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			n++
		}
	}
	if n == 0 {
		t.Fatal("一条迁移文件都没找到，测试路径不对")
	}
	return n
}

func TestMigrateCreatesAllTables(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "youlv.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	v, err := db.Migrate(context.Background(), d)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if want := migrationCount(t); v != want {
		t.Fatalf("schema 版本 = %d, want %d（每条迁移都应恰好应用一次）", v, want)
	}

	want := []string{
		"schema_version", "settings", "activities",
		"imports", "imports_files", "import_issues",
		"places", "visits", "trips", "weather", "tags", "visit_tags",
	}
	for _, name := range want {
		var got string
		if err := d.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got); err != nil {
			t.Errorf("表 %s 未创建: %v", name, err)
		}
	}
}

// TestMigrateIdempotent 迁移必须可重入：服务每次启动都会跑一遍。
func TestMigrateIdempotent(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "youlv.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	ctx := context.Background()

	for i := range 3 {
		if _, err := db.Migrate(ctx, d); err != nil {
			t.Fatalf("第 %d 次 Migrate: %v", i+1, err)
		}
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		t.Fatalf("统计版本记录: %v", err)
	}
	if want := migrationCount(t); n != want {
		t.Fatalf("schema_version 行数 = %d, want %d（迁移被重复应用了）", n, want)
	}
}

// TestMigrationSwitchesLegacyDefaultTiles 老版本把 CARTO 写成默认底图，
// 而它在国内多地连不通（瓦片超时，地图一片空白）。002 迁移只在
// 「用户从没改过底图」时把这两行清掉，好让新的默认值生效。
//
// 这条测试真的把版本号退回 v1 再迁移一次，而不是只看默认值：
// 已经装过老版本的库才是受影响的那些，新库本来就没有这两行。
func TestMigrationSwitchesLegacyDefaultTiles(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "youlv.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	ctx := context.Background()
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatalf("首轮 Migrate: %v", err)
	}

	// 模拟老版本留下的默认值。
	if _, err := d.Exec(`
		INSERT OR REPLACE INTO settings(key, value) VALUES
			('map.tiles_url', 'https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png'),
			('map.frame', 'wgs84'),
			('map.tiles_subdomains', 'a,b,c,d')`); err != nil {
		t.Fatalf("写入老默认值: %v", err)
	}
	// 退到 v1，让 002 再跑一遍。
	if _, err := d.Exec(`DELETE FROM schema_version WHERE version > 1`); err != nil {
		t.Fatalf("回退版本号: %v", err)
	}
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatalf("二轮 Migrate: %v", err)
	}

	for _, key := range []string{"map.tiles_url", "map.frame", "map.tiles_subdomains"} {
		var v string
		err := d.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
		if err == nil {
			t.Errorf("%s 应被清掉（让新默认值生效），实际还留着 %q", key, v)
		}
	}

	// 用户自己改过的底图不能被清掉。
	if _, err := d.Exec(`
		INSERT OR REPLACE INTO settings(key, value)
		VALUES('map.tiles_url', 'https://tile.example.com/{z}/{x}/{y}.png')`); err != nil {
		t.Fatalf("写入自定义底图: %v", err)
	}
	if _, err := d.Exec(`DELETE FROM schema_version WHERE version > 1`); err != nil {
		t.Fatalf("回退版本号: %v", err)
	}
	if _, err := db.Migrate(ctx, d); err != nil {
		t.Fatalf("三轮 Migrate: %v", err)
	}
	var got string
	if err := d.QueryRow(`SELECT value FROM settings WHERE key='map.tiles_url'`).Scan(&got); err != nil {
		t.Fatalf("自定义底图被删掉了: %v", err)
	}
	if got != "https://tile.example.com/{z}/{x}/{y}.png" {
		t.Errorf("自定义底图被改成了 %q", got)
	}
}

// TestForeignKeysEnforced 外键必须是真开着的：回滚逻辑依赖 ON DELETE 行为。
func TestForeignKeysEnforced(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "youlv.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if _, err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// visits.activity_id 指向不存在的活动时应当被拒。
	_, err = d.Exec(`INSERT INTO visits(place_name_raw, activity_raw, activity_id,
		arrived_at, arrived_local, local_date, local_month, local_year, local_hour,
		local_dow, tz, dedup_key) VALUES('x','y',999,0,'','','',0,0,0,'','k')`)
	if err == nil {
		t.Fatal("外键未生效：指向不存在活动的到访竟然插入成功")
	}
}

// TestPartialUniqueIndexForStubs 桩地点的部分唯一索引是 SQLite 特性，
// 一旦 schema 被改坏（比如换成了普通唯一索引）孤儿地点就会重复建桩。
func TestPartialUniqueIndexForStubs(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "youlv.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()
	if _, err := db.Migrate(context.Background(), d); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const ins = `INSERT INTO places(name, display_name, source_key, origin, is_stub)
	             VALUES(?, ?, ?, 'reference_only', 1)`
	if _, err := d.Exec(ins, "无名镇", "无名镇", "stub:1"); err != nil {
		t.Fatalf("首桩: %v", err)
	}
	if _, err := d.Exec(ins, "无名镇", "无名镇", "stub:2"); err == nil {
		t.Fatal("同名桩地点应被部分唯一索引挡下")
	}
	// 非桩地点同名是允许的（36 组重名分店）。
	if _, err := d.Exec(
		`INSERT INTO places(name, display_name, source_key) VALUES('同名点','同名点','a')`); err != nil {
		t.Fatalf("非桩重名应允许: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO places(name, display_name, source_key) VALUES('同名点','同名点','b')`); err != nil {
		t.Fatalf("非桩重名应允许: %v", err)
	}
}

func TestOpenReadOnlyMissingFile(t *testing.T) {
	if _, err := db.OpenReadOnly(filepath.Join(t.TempDir(), "nope.db")); err == nil {
		t.Fatal("只读打开不存在的库应当报错，而不是静默创建")
	}
}

// 备份契约（Felix2yu/.github/docs/backup.md）落在 `youlv backup` 上：
// 只读打开源库，VACUUM INTO 出快照写到 stdout。这条测试锁住两个性质：
// 只读连接**确实不能写**源库，但**必须能** VACUUM INTO。
//
// 这里曾经用 PRAGMA query_only(ON) 实现只读，结果 VACUUM INTO 被一并禁掉，
// 报 "attempt to write a readonly database"，备份功能整体不可用。
func TestOpenReadOnlyCanVacuumInto(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.db")

	w, err := db.Open(path)
	if err != nil {
		t.Fatalf("建库: %v", err)
	}
	if _, err := w.Exec(`CREATE TABLE t(x INTEGER)`); err != nil {
		t.Fatalf("建表: %v", err)
	}
	if _, err := w.Exec(`INSERT INTO t VALUES (1),(2),(3)`); err != nil {
		t.Fatalf("插入: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭: %v", err)
	}

	ro, err := db.OpenReadOnly(path)
	if err != nil {
		t.Fatalf("只读打开: %v", err)
	}
	defer ro.Close()

	snap := filepath.Join(dir, "snapshot.db")
	if _, err := ro.Exec(`VACUUM INTO ?`, snap); err != nil {
		t.Fatalf("只读连接上 VACUUM INTO 失败（备份会整体不可用）: %v", err)
	}

	// 快照必须真的可用、内容完整。
	need, err := db.Open(snap)
	if err != nil {
		t.Fatalf("打开快照: %v", err)
	}
	defer need.Close()
	var n int
	if err := need.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil {
		t.Fatalf("查快照: %v", err)
	}
	if n != 3 {
		t.Fatalf("快照行数 = %d，应为 3", n)
	}

	// 只读连接不能写源库，否则「备份不污染源库」这条不成立。
	if _, err := ro.Exec(`INSERT INTO t VALUES (4)`); err == nil {
		t.Fatal("只读连接竟然允许写入源库")
	}
}
