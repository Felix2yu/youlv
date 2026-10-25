package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"
)

// RollbackResult 是回滚的产出。
type RollbackResult struct {
	BatchNo string `json:"batch_no"`
	Deleted struct {
		Visits  int `json:"visits"`
		Trips   int `json:"trips"`
		Weather int `json:"weather"`
		Places  int `json:"places"`
		Tags    int `json:"tags"`
	} `json:"deleted"`
}

// Rollback 按批次撤销一次导入。
//
// 删除顺序与提交相反，且**不能**无差别删掉本批次创建的地点——用户可能已在
// 地点上补过备注、改过显示名，或后续批次又引用了它。因此只删「本批次创建
// 且零引用」的地点。
func (s *Service) Rollback(ctx context.Context, batchNo string) (*RollbackResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var importID int64
	var status string
	if err := tx.QueryRowContext(ctx,
		`SELECT id, status FROM imports WHERE batch_no=?`, batchNo).Scan(&importID, &status); err != nil {
		return nil, fmt.Errorf("批次 %s 不存在: %w", batchNo, err)
	}
	if status != StatusSuccess {
		return nil, UserErrf("批次 %s 当前状态为「%s」，只有已导入的批次才能撤销", batchNo, statusLabel(status))
	}

	res := &RollbackResult{BatchNo: batchNo}

	// 1. 标签关联随到访级联删除，但要先把本批次新增的孤立标签清掉。
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM visit_tags WHERE visit_id IN
			(SELECT id FROM visits WHERE import_batch_id=?)`, importID); err != nil {
		return nil, err
	}
	if res.Deleted.Visits, err = execRows(tx, ctx, `DELETE FROM visits WHERE import_batch_id=?`, importID); err != nil {
		return nil, err
	}
	if res.Deleted.Trips, err = execRows(tx, ctx, `DELETE FROM trips WHERE import_batch_id=?`, importID); err != nil {
		return nil, err
	}
	if res.Deleted.Weather, err = execRows(tx, ctx, `DELETE FROM weather WHERE import_batch_id=?`, importID); err != nil {
		return nil, err
	}

	// 2. 只删本批次建出、且已无人引用的地点。
	//
	// 判据用 NOT EXISTS 而不是 visit_count=0：计数器此刻还是提交时回填的旧值，
	// 拿它当条件会一个都删不掉。引用关系才是权威。
	if res.Deleted.Places, err = execRows(tx, ctx, `
		DELETE FROM places WHERE import_batch_id=?
		  AND NOT EXISTS (SELECT 1 FROM visits v WHERE v.place_id = places.id)
		  AND NOT EXISTS (SELECT 1 FROM trips t WHERE t.from_place_id = places.id
		                                        OR t.to_place_id = places.id)
		  AND NOT EXISTS (SELECT 1 FROM weather w WHERE w.place_id = places.id)`, importID); err != nil {
		return nil, err
	}
	if res.Deleted.Tags, err = execRows(tx, ctx, `
		DELETE FROM tags WHERE NOT EXISTS
			(SELECT 1 FROM visit_tags WHERE visit_tags.tag_id = tags.id)`); err != nil {
		return nil, err
	}

	// 3. 剩余地点的计数器要重算，否则会残留已删除到访的计数。
	if _, err := tx.ExecContext(ctx, `
		UPDATE places SET
			visit_count = COALESCE((SELECT COUNT(*) FROM visits v WHERE v.place_id = places.id),0),
			first_visit_at = (SELECT MIN(v.arrived_at) FROM visits v WHERE v.place_id = places.id),
			last_visit_at = (SELECT MAX(v.arrived_at) FROM visits v WHERE v.place_id = places.id)`); err != nil {
		return nil, err
	}

	// 4. 审计留痕：imports / imports_files / import_issues 一律保留。
	if _, err := tx.ExecContext(ctx,
		`UPDATE imports SET status=?, undone_at=unixepoch() WHERE id=?`,
		StatusRolledBack, importID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	log.Printf("[import] 已回滚 %s：到访 %d、交通 %d、天气 %d、地点 %d",
		batchNo, res.Deleted.Visits, res.Deleted.Trips, res.Deleted.Weather, res.Deleted.Places)
	return res, nil
}

// execRows 执行一条 DELETE/UPDATE 并返回影响行数。
//
// 失败**必须向上传播**：曾经这里返回 0 让事务照样提交，于是一次只删掉一半的
// 回滚会以「成功 + 0 条」收场，用户以为数据已经删干净了。
func execRows(tx *sql.Tx, ctx context.Context, q string, args ...any) (int, error) {
	res, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

// ---------- 查询 ----------

// Report 取回某批次的预检/执行报告。
func (s *Service) Report(ctx context.Context, batchNo string) (*Report, error) {
	var raw string
	var status string
	if err := s.db.QueryRowContext(ctx,
		`SELECT report_json, status FROM imports WHERE batch_no=?`, batchNo).
		Scan(&raw, &status); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrBatchNotFound, batchNo)
	}
	var rep Report
	if err := json.Unmarshal([]byte(raw), &rep); err != nil {
		return nil, fmt.Errorf("解析批次报告失败: %w", err)
	}
	rep.Status = status
	rep.normalizeSlices()
	return &rep, nil
}

// List 列出导入批次，最新的在前。
func (s *Service) List(ctx context.Context, limit int) ([]BatchSummary, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, batch_no, status, rows_total, rows_inserted, rows_skipped,
		       rows_failed, created_at, finished_at, duration_ms, error_summary
		FROM imports ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BatchSummary
	for rows.Next() {
		var b BatchSummary
		var fin, dur *int64
		var errStr *string
		if err := rows.Scan(&b.ID, &b.BatchNo, &b.Status, &b.RowsTotal, &b.Inserted,
			&b.Skipped, &b.Failed, &b.CreatedAt, &fin, &dur, &errStr); err != nil {
			return nil, err
		}
		b.FinishedAt, b.DurationMS = fin, dur
		if errStr != nil {
			b.Error = *errStr
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// BatchSummary 是批次列表里的一条。
type BatchSummary struct {
	ID         int64  `json:"id"`
	BatchNo    string `json:"batch_no"`
	Status     string `json:"status"`
	RowsTotal  int    `json:"rows_total"`
	Inserted   int    `json:"rows_inserted"`
	Skipped    int    `json:"rows_skipped"`
	Failed     int    `json:"rows_failed"`
	CreatedAt  int64  `json:"created_at"`
	FinishedAt *int64 `json:"finished_at,omitempty"`
	DurationMS *int64 `json:"duration_ms,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Cleanup 清掉超过 maxAge 的暂存目录。服务启动时调用一次。
func (s *Service) Cleanup(maxAge time.Duration) int { return cleanupStaged(s.tmpDir, maxAge) }

// ErrBatchHasData 在 errors.go 定义（与 ErrBatchNotFound 放在一起）。
//
// statusLabel 把内部状态值翻成给用户看的说法。
func statusLabel(s string) string {
	switch s {
	case StatusPreview:
		return "待确认"
	case StatusRunning:
		return "导入中"
	case StatusSuccess:
		return "已导入"
	case StatusFailed:
		return "已失败"
	case StatusRolledBack:
		return "已撤销"
	}
	return s
}

// DeleteBatch 删除一个批次的**审计记录**（不碰业务数据）。
//
// 与 Rollback 的分工：
//   - Rollback 删业务数据，保留审计行（status 置 rolled_back）；
//   - DeleteBatch 删审计行本身（imports 行 + 级联的 imports_files / import_issues）。
//
// 已成功导入的批次不允许直接删：visits.import_batch_id 是指向 imports 的外键，
// 删掉 imports 行会让它被 SET NULL，此后这批数据就再也按批次回滚不掉了。
// 因此要先撤销（Rollback）再删记录。
func (s *Service) DeleteBatch(ctx context.Context, batchNo string) error {
	var importID int64
	var status string
	if err := s.db.QueryRowContext(ctx,
		`SELECT id, status FROM imports WHERE batch_no=?`, batchNo).Scan(&importID, &status); err != nil {
		return fmt.Errorf("%w: %s", ErrBatchNotFound, batchNo)
	}
	if status == StatusSuccess {
		return fmt.Errorf("%w：批次 %s", ErrBatchHasData, batchNo)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// imports_files / import_issues 的 import_id 带 ON DELETE CASCADE，
	// 删掉 imports 行它们会一并消失。
	if _, err := tx.ExecContext(ctx, `DELETE FROM imports WHERE id=?`, importID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// 暂存目录是预检产物，一并清掉。
	if err := os.RemoveAll(s.stageDir(batchNo)); err != nil {
		return err
	}
	return nil
}
