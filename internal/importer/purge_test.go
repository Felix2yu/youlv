package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 清空要把业务数据、批次审计、暂存目录一起清干净，但设置项必须留着。
func TestPurgeClearsDataAndAudit(t *testing.T) {
	dir := dataDir(t)
	s := newSvc(t)
	ctx := context.Background()

	// 先写一条设置，用来验证清空不会连配置一起抹掉。
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES('appearance.theme','dark')`); err != nil {
		t.Fatalf("写设置失败: %v", err)
	}

	var files []UploadedFile
	for _, n := range []string{"places.csv", "visits.csv", "trips.csv", "weather.csv"} {
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			t.Skipf("缺少样本 %s: %v", n, err)
		}
		files = append(files, UploadedFile{Name: n, Size: int64(len(b)), Content: b})
	}

	rep, err := s.Preview(ctx, files, DefaultOptions())
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := s.Commit(ctx, rep.BatchNo, Decisions{}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if n := countOf(t, s, "visits"); n == 0 {
		t.Fatal("提交后应当有到访记录")
	}
	if _, err := os.Stat(s.stageDir(rep.BatchNo)); err != nil {
		t.Fatalf("提交后暂存目录应存在: %v", err)
	}

	res, err := s.Purge(ctx)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}

	for _, table := range []string{
		"visits", "trips", "weather", "places", "tags", "activities",
		"visit_tags", "imports", "imports_files", "import_issues",
	} {
		if n := countOf(t, s, table); n != 0 {
			t.Errorf("清空后 %s 仍剩 %d 行", table, n)
		}
	}

	var theme string
	if err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key='appearance.theme'`).Scan(&theme); err != nil {
		t.Fatalf("设置项应保留: %v", err)
	}
	if theme != "dark" {
		t.Errorf("设置值被改了: %q", theme)
	}

	if entries, err := os.ReadDir(s.tmpDir); err == nil && len(entries) != 0 {
		t.Errorf("暂存目录应清空，仍剩 %d 项", len(entries))
	}

	if res.Deleted.Visits == 0 || res.Deleted.Batches == 0 || res.Deleted.Places == 0 {
		t.Errorf("返回的删除计数不该是 0: %+v", res.Deleted)
	}

	// 清空后必须能从头再导一遍：ready 应与首次预检一致（幂等的前提）。
	again, err := s.Preview(ctx, files, DefaultOptions())
	if err != nil {
		t.Fatalf("清空后 Preview: %v", err)
	}
	if again.Totals.Ready != rep.Totals.Ready {
		t.Errorf("清空后重新预检 ready=%d，首次是 %d —— 清得不干净",
			again.Totals.Ready, rep.Totals.Ready)
	}

	// 空库上再清一次不应报错，也不应留下脏数据。
	if _, err := s.Purge(ctx); err != nil {
		t.Fatalf("空库 Purge 应当成功: %v", err)
	}
}
