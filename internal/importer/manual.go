package importer

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Felix2yu/youlv/internal/geo"
)

// 本文件是「手动录入」：不经过 CSV，直接往库里写一条到访或一个地点。
//
// 写侧按仓库约定仍然全部落在 importer 包里，而不是散到 api 层——
// 这样「数据是怎么进库的」永远只有一处可看，去重键、坐标派生、地点统计
// 回算这些规则也只会有一份实现。

// ManualVisit 是手动填写的一次到访。
type ManualVisit struct {
	PlaceName  string // 已存在的地点名，或要新建的地点名
	PlaceID    int64  // 有则优先按 id 定位
	ArrivedAt  int64  // unix 秒
	DepartedAt *int64
	Activity   string
	Note       string
	Tags       []string
	Lat        *float64 // 新建地点时的坐标（WGS-84）
	Lon        *float64
	City       string
}

// ManualVisitResult 是手动写入的产出。
type ManualVisitResult struct {
	VisitID  int64  `json:"visit_id"`
	PlaceID  int64  `json:"place_id"`
	Created  bool   `json:"created_place"` // 顺带新建了地点
	Duration *int64 `json:"duration_s,omitempty"`
}

// CreateVisit 手动写入一条到访。地点不存在时按给定坐标（或纯名字桩）新建。
func (s *Service) CreateVisit(ctx context.Context, m ManualVisit) (*ManualVisitResult, error) {
	if m.PlaceName == "" && m.PlaceID == 0 {
		return nil, UserErrf("地点名不能为空")
	}
	if m.ArrivedAt <= 0 {
		return nil, UserErrf("抵达时间不合法")
	}
	if m.DepartedAt != nil && *m.DepartedAt < m.ArrivedAt {
		return nil, UserErrf("离开时间早于抵达时间")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	placeID, created, err := s.resolveManualPlace(ctx, tx, m)
	if err != nil {
		return nil, err
	}

	arr := time.Unix(m.ArrivedAt, 0).In(s.tz)
	var dep *int64
	var dur *int64
	if m.DepartedAt != nil {
		dep = m.DepartedAt
		d := *m.DepartedAt - m.ArrivedAt
		dur = &d
	}
	lat, lon := 0.0, 0.0
	if m.Lat != nil && m.Lon != nil {
		lat, lon = geo.Quantize(*m.Lat, 6), geo.Quantize(*m.Lon, 6)
	}
	var latPtr, lonPtr, gcjLatPtr, gcjLonPtr *float64
	if lat != 0 || lon != 0 {
		gLat, gLon := geo.WGS84ToGCJ02(lat, lon)
		latPtr, lonPtr = &lat, &lon
		gLat, gLon = geo.Quantize(gLat, 6), geo.Quantize(gLon, 6)
		gcjLatPtr, gcjLonPtr = &gLat, &gLon
	}

	key := visitDedupKey(fmt.Sprintf("manual:%d", placeID), m.ArrivedAt, dep, lat, lon)
	tags := strings.Join(m.Tags, ",")
	res, err := tx.ExecContext(ctx, `
		INSERT INTO visits(place_id, place_name_raw, activity_raw,
			arrived_at, departed_at, duration_s, tz, arrived_local, departed_local,
			local_date, local_month, local_year, local_hour, local_dow,
			lat, lon, lat_gcj, lon_gcj, coord_frame,
			resolve_method, resolve_confidence, note, tags_raw, dedup_key)
		VALUES(?,?,?,?,?,?,'Asia/Shanghai',?,?,?,?,?,?,?,?,?,?,?,'wgs84','manual','high',?,?,?)
		ON CONFLICT(dedup_key) DO NOTHING`,
		placeID, m.PlaceName, m.Activity, m.ArrivedAt, dep, dur,
		arr.Format("2006-01-02 15:04"), depLocal(arr, dep, s.tz),
		arr.Format("2006-01-02"), arr.Format("2006-01"), arr.Year(), arr.Hour(), int(arr.Weekday()),
		latPtr, lonPtr, gcjLatPtr, gcjLonPtr,
		nullStr(m.Note), nullStr(tags), key)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, UserErrf("这条到访已经记过了（同一地点同一时刻）")
	}
	visitID, _ := res.LastInsertId()

	if err := s.linkTags(ctx, tx, visitID, m.Tags); err != nil {
		return nil, err
	}
	if err := backfillOnePlace(ctx, tx, placeID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ManualVisitResult{VisitID: visitID, PlaceID: placeID, Created: created, Duration: dur}, nil
}

// UpdateVisit 修改一条手动（或导入）到访的备注、标签、活动与时刻。
func (s *Service) UpdateVisit(ctx context.Context, id int64, p VisitPatch) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var placeID int64
	if err := tx.QueryRowContext(ctx, `SELECT place_id FROM visits WHERE id=?`, id).
		Scan(&placeID); err != nil {
		return fmt.Errorf("%w: %d", ErrBatchNotFound, id)
	}
	if p.Note != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE visits SET note=? WHERE id=?`, nullStr(*p.Note), id); err != nil {
			return err
		}
	}
	if p.Activity != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE visits SET activity_raw=? WHERE id=?`, *p.Activity, id); err != nil {
			return err
		}
	}
	if p.Tags != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE visits SET tags_raw=? WHERE id=?`,
			nullStr(strings.Join(*p.Tags, ",")), id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM visit_tags WHERE visit_id=?`, id); err != nil {
			return err
		}
		if err := s.linkTags(ctx, tx, id, *p.Tags); err != nil {
			return err
		}
	}
	if p.DepartedAt != nil {
		// 0 是「清掉离开时间」的约定：JSON 里没法用 null 表达「显式清空」
		// （null 已经被解释成「这个字段不改」），拿 0 当哨兵最省事。
		if *p.DepartedAt == 0 {
			if _, err := tx.ExecContext(ctx,
				`UPDATE visits SET departed_at=NULL, departed_local='', duration_s=0 WHERE id=?`, id); err != nil {
				return err
			}
		} else {
			var arrived int64
			if err := tx.QueryRowContext(ctx, `SELECT arrived_at FROM visits WHERE id=?`, id).
				Scan(&arrived); err != nil {
				return err
			}
			if *p.DepartedAt < arrived {
				return UserErrf("离开时间早于抵达时间")
			}
			dur := *p.DepartedAt - arrived
			depLocal := time.Unix(*p.DepartedAt, 0).In(s.tz).Format("2006-01-02 15:04")
			if _, err := tx.ExecContext(ctx,
				`UPDATE visits SET departed_at=?, departed_local=?, duration_s=? WHERE id=?`,
				*p.DepartedAt, depLocal, dur, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// VisitPatch 是允许修改的字段；nil 表示不改。
type VisitPatch struct {
	Note       *string
	Activity   *string
	Tags       *[]string
	DepartedAt *int64
}

// DeleteVisit 删除单条到访，并顺带清掉不再被引用的标签。
func (s *Service) DeleteVisit(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var placeID int64
	if err := tx.QueryRowContext(ctx, `SELECT place_id FROM visits WHERE id=?`, id).
		Scan(&placeID); err != nil {
		return fmt.Errorf("%w: %d", ErrBatchNotFound, id)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM visit_tags WHERE visit_id=?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM visits WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %d", ErrBatchNotFound, id)
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM tags WHERE NOT EXISTS
			(SELECT 1 FROM visit_tags WHERE visit_tags.tag_id = tags.id)`); err != nil {
		return err
	}
	if err := backfillOnePlace(ctx, tx, placeID); err != nil {
		return err
	}
	// 手录时顺手建的地点，删掉最后一条到访后就只剩个空壳（0 次到访、
	// 占着地点库和城市统计）。只清 origin='manual' 的：导入产生的桩地点
	// 由回滚链路自己管，这里插手会破坏 source_key 的稳定性。
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM places
		 WHERE id=? AND origin='manual'
		   AND NOT EXISTS (SELECT 1 FROM visits v WHERE v.place_id = places.id)
		   AND NOT EXISTS (SELECT 1 FROM trips t WHERE t.from_place_id = places.id
		                                             OR t.to_place_id = places.id)
		   AND NOT EXISTS (SELECT 1 FROM weather w WHERE w.place_id = places.id)`,
		placeID); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- 内部 ----------

// resolveManualPlace 找到或新建地点。
func (s *Service) resolveManualPlace(ctx context.Context, tx *sql.Tx, m ManualVisit) (int64, bool, error) {
	if m.PlaceID > 0 {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM places WHERE id=?`, m.PlaceID).
			Scan(&n); err != nil || n == 0 {
			return 0, false, UserErrf("地点 %d 不存在", m.PlaceID)
		}
		return m.PlaceID, false, nil
	}
	var id int64
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM places WHERE name=? ORDER BY is_stub, id LIMIT 1`, m.PlaceName).Scan(&id)
	if err == nil {
		return id, false, nil
	}

	// 库里没有：按给的信息建一个，origin 标 manual，和导入进来的区分开。
	hasCoord := m.Lat != nil && m.Lon != nil
	lat, lon := 0.0, 0.0
	if hasCoord {
		lat, lon = geo.Quantize(*m.Lat, 6), geo.Quantize(*m.Lon, 6)
	}
	key := PlaceSourceKey(m.PlaceName, lat, lon, hasCoord)
	if !hasCoord {
		key = "stub:" + hashString(m.PlaceName)
	}
	var latPtr, lonPtr, gLatPtr, gLonPtr *float64
	if hasCoord {
		// 地点表也要落 GCJ-02 渲染副本：只写 WGS 会让地点库与地图上的
		// 点位相差几百米（AGENTS.md 的坐标双列表约定对 places 同样成立）。
		gLat, gLon := geo.WGS84ToGCJ02(lat, lon)
		gLat, gLon = geo.Quantize(gLat, 6), geo.Quantize(gLon, 6)
		latPtr, lonPtr = &lat, &lon
		gLatPtr, gLonPtr = &gLat, &gLon
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO places(name, name_normalized, display_name, source_key,
			city, lat, lon, lat_gcj, lon_gcj, origin, is_stub)
		VALUES(?,?,?,?,?,?,?,?,?,'manual',?)
		ON CONFLICT DO NOTHING`,
		m.PlaceName, m.PlaceName, m.PlaceName, key, nullStr(m.City),
		latPtr, lonPtr, gLatPtr, gLonPtr, boolInt(!hasCoord))
	if err != nil {
		return 0, false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		id, _ = res.LastInsertId()
		return id, true, nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM places WHERE source_key=?`, key).Scan(&id); err != nil {
		return 0, false, err
	}
	return id, false, nil
}

// backfillOnePlace 重算单个地点的计数与首末到访。
func backfillOnePlace(ctx context.Context, tx *sql.Tx, placeID int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE places SET
			visit_count = COALESCE((SELECT COUNT(*) FROM visits v WHERE v.place_id = places.id),0),
			first_visit_at = (SELECT MIN(v.arrived_at) FROM visits v WHERE v.place_id = places.id),
			last_visit_at = (SELECT MAX(v.arrived_at) FROM visits v WHERE v.place_id = places.id)
		WHERE id=?`, placeID)
	return err
}

func depLocal(arr time.Time, dep *int64, tz *time.Location) any {
	if dep == nil {
		return nil
	}
	return time.Unix(*dep, 0).In(tz).Format("2006-01-02 15:04")
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
