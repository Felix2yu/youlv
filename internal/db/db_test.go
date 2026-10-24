package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/yufei/youlv/internal/db"
)

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
	if v != 1 {
		t.Fatalf("schema 版本 = %d, want 1", v)
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
	if n != 1 {
		t.Fatalf("schema_version 行数 = %d, want 1（迁移被重复应用了）", n)
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
