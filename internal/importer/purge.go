package importer

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// PurgeResult 是清空的产出：删掉了多少东西，让人事后能核对。
type PurgeResult struct {
	Deleted struct {
		Visits     int `json:"visits"`
		Trips      int `json:"trips"`
		Weather    int `json:"weather"`
		Places     int `json:"places"`
		Tags       int `json:"tags"`
		Activities int `json:"activities"`
		Batches    int `json:"batches"`
		Issues     int `json:"issues"`
	} `json:"deleted"`
}

// Purge 清空全部导入数据与批次记录，把库退回「刚装好」的状态。
//
// 与 Rollback 的分工：Rollback 只撤一批、且保留审计，用于误导入的局部回退；
// Purge 是把数据整个倒掉重来（换了导出源、或想用新的消歧规则重导一遍）。
//
// settings 与 schema_version 不动 —— 那是配置和库版本，不属于数据。
// 删除在一个事务里完成：要么清干净，要么一条都不动，不会留下「地点没了、
// 到访还在」的半截状态。
func (s *Service) Purge(ctx context.Context) (*PurgeResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res := &PurgeResult{}
	count := func(table string) int {
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
			return 0
		}
		return n
	}
	res.Deleted.Visits = count("visits")
	res.Deleted.Trips = count("trips")
	res.Deleted.Weather = count("weather")
	res.Deleted.Places = count("places")
	res.Deleted.Tags = count("tags")
	res.Deleted.Activities = count("activities")
	res.Deleted.Batches = count("imports")
	res.Deleted.Issues = count("import_issues")

	// 删除顺序：先断关联表与引用方，再从叶子删到根。
	// visits/trips/weather 都引用 places，places 引用 imports，故 imports 最后删。
	for _, q := range []string{
		`DELETE FROM visit_tags`,
		`DELETE FROM visits`,
		`DELETE FROM trips`,
		`DELETE FROM weather`,
		`DELETE FROM places`,
		`DELETE FROM tags`,
		`DELETE FROM activities`,
		`DELETE FROM import_issues`,
		`DELETE FROM imports_files`,
		`DELETE FROM imports`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return nil, fmt.Errorf("清空失败（%s）: %w", q, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// 预检暂存已无对应批次，留着也没人能读，一并清掉。
	if entries, err := os.ReadDir(s.tmpDir); err == nil {
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(s.tmpDir, e.Name()))
		}
	}

	// VACUUM 让文件真的变小：删行只会在文件里留下空洞。
	// 它需要独占锁，失败也无妨 —— 数据已经清干净了，这只是收尾。
	if _, err := s.db.ExecContext(ctx, "VACUUM"); err != nil {
		log.Printf("[import] 清空后 VACUUM 跳过: %v", err)
	}

	log.Printf("[import] 已清空全部数据：到访 %d、交通 %d、天气 %d、地点 %d、批次 %d、问题 %d",
		res.Deleted.Visits, res.Deleted.Trips, res.Deleted.Weather,
		res.Deleted.Places, res.Deleted.Batches, res.Deleted.Issues)
	return res, nil
}
