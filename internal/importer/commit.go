package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/yufei/youlv/internal/csvin"
	"github.com/yufei/youlv/internal/geo"
	"github.com/yufei/youlv/internal/store"
)

// Commit 读取预检落下的暂存产物，在单个事务内写入全部业务表。
//
// 为什么是整批一个事务而不是按文件分事务：到访依赖地点，若地点文件失败而
// 到访成功，地点解析会全部退化为「自动建点」，凭空造出几百个假地点。半成功
// 状态比整批失败更难收拾，所以要么全进，要么全不进。
func (s *Service) Commit(ctx context.Context, batchNo string, d Decisions) (*Report, error) {
	started := time.Now()
	dir := s.stageDir(batchNo)

	places, err := readStaged[stagedPlace](filepath.Join(dir, stageFileName(csvin.KindPlaces)))
	if err != nil {
		return nil, err
	}
	visits, err := readStaged[stagedVisit](filepath.Join(dir, stageFileName(csvin.KindVisits)))
	if err != nil {
		return nil, err
	}
	trips, err := readStaged[stagedTrip](filepath.Join(dir, stageFileName(csvin.KindTrips)))
	if err != nil {
		return nil, err
	}
	weather, err := readStaged[stagedWeather](filepath.Join(dir, stageFileName(csvin.KindWeather)))
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	// 任何路径只要没走到 tx.Commit 就整体回滚。
	defer tx.Rollback()

	var importID int64
	var optJSON string
	if err := tx.QueryRowContext(ctx,
		`SELECT id, options_json FROM imports WHERE batch_no=?`, batchNo).Scan(&importID, &optJSON); err != nil {
		return nil, fmt.Errorf("批次 %s 不存在: %w", batchNo, err)
	}
	var opt Options
	_ = json.Unmarshal([]byte(optJSON), &opt)
	if d.GlobalStrategy != "" {
		opt.DupStrategy = d.GlobalStrategy
	}
	if d.WeatherStrategy != "" {
		opt.WeatherDupStrategy = d.WeatherStrategy
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE imports SET status=?, started_at=unixepoch() WHERE id=?`, StatusRunning, importID); err != nil {
		return nil, err
	}

	stats := map[csvin.Kind]*[3]int{} // inserted, skipped, failed

	// 固定顺序：activities → places → visits → visit_tags → trips → weather
	activityIDs, err := s.commitActivities(ctx, tx, visits)
	if err != nil {
		return nil, err
	}
	placeIDs, ins, skip, promoted, err := s.commitPlaces(ctx, tx, places, importID, opt)
	if err != nil {
		return nil, err
	}
	stats[csvin.KindPlaces] = &[3]int{ins, skip, 0}

	vi, vs, vf, err := s.commitVisits(ctx, tx, visits, placeIDs, activityIDs, importID, opt)
	if err != nil {
		return nil, err
	}
	stats[csvin.KindVisits] = &[3]int{vi, vs, vf}

	ti, ts, tf, err := s.commitTrips(ctx, tx, trips, placeIDs, importID, opt)
	if err != nil {
		return nil, err
	}
	stats[csvin.KindTrips] = &[3]int{ti, ts, tf}

	wi, ws, wf, err := s.commitWeather(ctx, tx, weather, placeIDs, importID, opt, d)
	if err != nil {
		return nil, err
	}
	stats[csvin.KindWeather] = &[3]int{wi, ws, wf}

	if err := s.backfillPlaceStats(ctx, tx); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	stJSON, _ := json.Marshal(stats)
	if _, err := tx.ExecContext(ctx,
		`UPDATE imports SET status=?, finished_at=?, duration_ms=?, stats_json=? WHERE id=?`,
		StatusSuccess, now, time.Since(started).Milliseconds(), string(stJSON), importID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	log.Printf("[import] 提交完成 %s：地点 %d、到访 %d、交通 %d、天气 %d，耗时 %d ms",
		batchNo, ins, vi, ti, wi, time.Since(started).Milliseconds())

	rep, err := s.Report(ctx, batchNo)
	if err != nil {
		return nil, err
	}
	// Files 级回填后要重新汇总 Totals：Report 是从预检时存的 report_json
	// 反序列化来的，那份 Totals 里 Inserted/Skipped 还是 0，不重算的话
	// 结果页会显示「写入 0 条」，和下面每一行的数字对不上。
	rep.Totals.Inserted, rep.Totals.Skipped = 0, 0
	for i := range rep.Files {
		if st, ok := stats[rep.Files[i].Kind]; ok {
			rep.Files[i].Inserted, rep.Files[i].Skipped = st[0], st[1]
		}
		if rep.Files[i].Kind == csvin.KindPlaces {
			rep.Files[i].Promoted = promoted
		}
		rep.Totals.Inserted += rep.Files[i].Inserted
		rep.Totals.Skipped += rep.Files[i].Skipped
	}
	rep.Totals.Promoted = promoted
	rep.Status = StatusSuccess
	return rep, nil
}

func (s *Service) commitActivities(ctx context.Context, tx *sql.Tx, visits []stagedVisit) (map[string]int64, error) {
	out := make(map[string]int64)
	seen := make(map[string]bool)
	for _, v := range visits {
		if v.ActivityRaw == "" || seen[v.ActivityRaw] {
			continue
		}
		seen[v.ActivityRaw] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO activities(name, sort_order, color) VALUES(?,?,?)
			 ON CONFLICT(name) DO UPDATE SET color=CASE WHEN activities.color=? THEN excluded.color ELSE activities.color END`,
			v.ActivityRaw, len(out), store.ActivityColor(len(out)), store.DefaultActivityColor); err != nil {
			return nil, fmt.Errorf("写入活动字典失败: %w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, name FROM activities`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}

func (s *Service) commitPlaces(ctx context.Context, tx *sql.Tx, rows []stagedPlace,
	importID int64, opt Options) (map[string]int64, int, int, int, error) {
	ids := make(map[string]int64)
	if e, err := s.loadPlaceIDs(ctx, tx); err == nil {
		for k, v := range e {
			ids[k] = v
		}
	}
	var inserted, skipped, promoted int
	for _, p := range rows {
		if id, exists := ids[p.SourceKey]; exists {
			if p.Promote {
				n, err := s.promotePlace(ctx, tx, id, p)
				if err != nil {
					return nil, 0, 0, 0, err
				}
				if n > 0 {
					promoted++
					continue
				}
				// 已经不是桩了（被别的批次升过）→ 按「已存在」处理。
			}
			skipped++
			continue
		}
		res, err := tx.ExecContext(ctx, `
			INSERT INTO places(source_key, name, display_name, lat, lon, lat_gcj, lon_gcj,
				coord_frame, radius_m, rating, country, admin_area, sub_admin_area, city,
				district, street, house_number, origin, is_stub, import_batch_id, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,unixepoch())
			ON CONFLICT(source_key) DO NOTHING`,
			p.SourceKey, p.Name, p.DisplayName, p.Lat, p.Lon, p.LatGCJ, p.LonGCJ,
			p.CoordFrame, p.RadiusM, p.Rating, p.Country, p.AdminArea, p.SubAdmin,
			p.City, p.District, p.Street, p.HouseNumber, p.Origin, boolInt(p.IsStub), importID)
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("写入地点「%s」失败: %w", p.Name, err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			skipped++
			continue
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, 0, 0, 0, err
		}
		ids[p.SourceKey] = id
		inserted++
	}
	return ids, inserted, skipped, promoted, nil
}

// promotePlace 把既有地点升级为正式地点，并回填引用它的到访/交通的空坐标。
//
// 「既有地点」有两种：只有名字的桩（此前仅被提到过），与先前由到访坐标
// 派生出来的行（origin=visit_derived）。后者是分批导入场景：先导到访、
// 后导地点时，同一地点会因坐标系不同而算出两个 key，靠这里并回一行。
//
// 幂等闸门是 WHERE 里的 (is_stub = 1 OR origin <> 'imported')：已升级过
// 的行再执行一次命中 0 行，不会重复改、也不会重复计数。
//
// 回滚语义（重要）：升级改的是**另一个批次**创建的行，因此「撤销升级批次」
// 不会把坐标收回去；撤销创建批次时，该行因被后续批次的到访/交通引用而保留
// （回滚只删本批次写入且无人引用的行），坐标也一并留下。
// 一句话：回滚撤销的是「本批次写入的行」，不追溯撤销「本批次对更早批次的
// 行所做的就地修改」。
func (s *Service) promotePlace(ctx context.Context, tx *sql.Tx, id int64, p stagedPlace) (int64, error) {
	if p.Lat == nil || p.Lon == nil {
		return 0, nil
	}
	gLat, gLon := geo.WGS84ToGCJ02(*p.Lat, *p.Lon)
	gLat, gLon = geo.Quantize(gLat, 6), geo.Quantize(gLon, 6)

	res, err := tx.ExecContext(ctx, `
		UPDATE places SET lat=?, lon=?, lat_gcj=?, lon_gcj=?, coord_frame=?,
			radius_m=COALESCE(?, radius_m), rating=COALESCE(?, rating),
			country=COALESCE(?, country), admin_area=COALESCE(?, admin_area),
			sub_admin_area=COALESCE(?, sub_admin_area), city=COALESCE(?, city),
			district=COALESCE(?, district), street=COALESCE(?, street),
			house_number=COALESCE(?, house_number),
			origin='imported', is_stub=0, updated_at=unixepoch()
		WHERE id=? AND (is_stub = 1 OR origin <> 'imported')`,
		p.Lat, p.Lon, p.LatGCJ, p.LonGCJ, p.CoordFrame,
		p.RadiusM, p.Rating, p.Country, p.AdminArea, p.SubAdmin,
		p.City, p.District, p.Street, p.HouseNumber, id)
	if err != nil {
		return 0, fmt.Errorf("升级桩地点「%s」失败: %w", p.Name, err)
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return 0, err
	}

	// 回填引用该地点的到访：只补坐标为空的行。
	//
	// 已有自身定位的到访保持不动——那是一手信息，比地点表的登记点更准。
	// lat_raw/lon_raw 也不动：那是 CSV 原始值的审计快照，回填会伪造原始值。
	if _, err := tx.ExecContext(ctx, `
		UPDATE visits SET lat=?, lon=?, lat_gcj=?, lon_gcj=?, coord_frame='wgs84'
		WHERE place_id=? AND (lat IS NULL OR lon IS NULL)`,
		p.Lat, p.Lon, gLat, gLon, id); err != nil {
		return 0, err
	}
	// 交通的起终点同理：桩没有坐标时，这两列也是空的。
	if _, err := tx.ExecContext(ctx, `
		UPDATE trips SET from_lat=?, from_lon=?, from_lat_gcj=?, from_lon_gcj=?
		WHERE from_place_id=? AND (from_lat IS NULL OR from_lon IS NULL)`,
		p.Lat, p.Lon, gLat, gLon, id); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE trips SET to_lat=?, to_lon=?, to_lat_gcj=?, to_lon_gcj=?
		WHERE to_place_id=? AND (to_lat IS NULL OR to_lon IS NULL)`,
		p.Lat, p.Lon, gLat, gLon, id); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Service) loadPlaceIDs(ctx context.Context, tx *sql.Tx) (map[string]int64, error) {
	out := make(map[string]int64)
	rows, err := tx.QueryContext(ctx, `SELECT id, source_key FROM places`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var k string
		if err := rows.Scan(&id, &k); err != nil {
			return nil, err
		}
		out[k] = id
	}
	return out, rows.Err()
}

func (s *Service) commitVisits(ctx context.Context, tx *sql.Tx, rows []stagedVisit,
	placeIDs map[string]int64, activityIDs map[string]int64, importID int64, opt Options) (int, int, int, error) {
	var inserted, skipped, failed int
	for _, v := range rows {
		pid := placeIDs[v.PlaceKey]
		if pid == 0 {
			failed++
			continue
		}
		var aid *int64
		if id, ok := activityIDs[v.ActivityRaw]; ok {
			aid = &id
		}

		// skip 策略用 DO NOTHING；overwrite 用 DO UPDATE 覆盖可变字段。
		// 到访的 dedup_key 已包含地点与时刻，重复即同一条事实，没有冲突可言。
		q := `INSERT INTO visits(place_id, place_name_raw, activity_id, activity_raw,
			arrived_at, departed_at, duration_s, tz, arrived_local, departed_local,
			local_date, local_month, local_year, local_hour, local_dow,
			lat, lon, lat_gcj, lon_gcj, lat_raw, lon_raw, coord_frame,
			resolve_method, resolve_dist_m, resolve_confidence,
			note, tags_raw, dedup_key, import_batch_id)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(dedup_key) DO NOTHING`
		if opt.DupStrategy == "overwrite" {
			q = strings.Replace(q, "DO NOTHING",
				`DO UPDATE SET activity_id=excluded.activity_id, note=excluded.note,
				 tags_raw=excluded.tags_raw, coord_frame=excluded.coord_frame,
				 resolve_method=excluded.resolve_method`, 1)
		}
		res, err := tx.ExecContext(ctx, q,
			pid, v.PlaceNameRaw, aid, v.ActivityRaw,
			v.ArrivedAt, v.DepartedAt, v.DurationS, v.TZ, v.ArrivedLocal, v.DepartedLocal,
			v.LocalDate, v.LocalMonth, v.LocalYear, v.LocalHour, v.LocalDow,
			v.Lat, v.Lon, v.LatGCJ, v.LonGCJ, v.LatRaw, v.LonRaw, v.CoordFrame,
			v.Method, v.DistM, v.Confidence,
			v.Note, v.TagsRaw, v.DedupKey, importID)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("写入到访失败（第 %d 行）: %w", v.RowNo, err)
		}
		n, err := res.RowsAffected()
		if err == nil && n == 0 {
			skipped++
			continue
		}
		vid, err := res.LastInsertId()
		if err != nil {
			return 0, 0, 0, err
		}
		if err := s.linkTags(ctx, tx, vid, v.Tags); err != nil {
			return 0, 0, 0, err
		}
		inserted++
	}
	return inserted, skipped, failed, nil
}

func (s *Service) linkTags(ctx context.Context, tx *sql.Tx, visitID int64, tags []string) error {
	for _, t := range tags {
		// 标签颜色此前是个永远为空的死列（表里有 color，前端拿到的却是空串）。
		// 这里按标签在字典里的序号取调色板色：同一标签在任何批次导入
		// 都得到同一个颜色（序号来自 id，跨批次稳定）。
		var seq int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE((SELECT COUNT(*) FROM tags WHERE name <= ?), 0)`, t).Scan(&seq); err != nil {
			return fmt.Errorf("统计标签序号失败: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tags(name, color) VALUES(?,?)
			 ON CONFLICT(name) DO UPDATE SET color=CASE WHEN tags.color='' THEN excluded.color ELSE tags.color END`,
			t, store.TagColor(seq)); err != nil {
			return fmt.Errorf("写入标签失败: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO visit_tags(visit_id, tag_id)
			SELECT ?, id FROM tags WHERE name = ?
			ON CONFLICT DO NOTHING`, visitID, t); err != nil {
			return fmt.Errorf("关联标签失败: %w", err)
		}
	}
	return nil
}

func (s *Service) commitTrips(ctx context.Context, tx *sql.Tx, rows []stagedTrip,
	placeIDs map[string]int64, importID int64, opt Options) (int, int, int, error) {
	var inserted, skipped, failed int
	for _, t := range rows {
		var fp, tp *int64
		if id := placeIDs[t.FromPlaceKey]; id != 0 {
			fp = &id
		}
		if id := placeIDs[t.ToPlaceKey]; id != 0 {
			tp = &id
		}
		var fl, flo, flg, flog, tl, tlo, tlg, tlog *float64
		if fp != nil {
			if err := tx.QueryRowContext(ctx,
				`SELECT lat, lon, lat_gcj, lon_gcj FROM places WHERE id=?`, *fp).
				Scan(&fl, &flo, &flg, &flog); err != nil {
				return 0, 0, 0, err
			}
		}
		if tp != nil {
			if err := tx.QueryRowContext(ctx,
				`SELECT lat, lon, lat_gcj, lon_gcj FROM places WHERE id=?`, *tp).
				Scan(&tl, &tlo, &tlg, &tlog); err != nil {
				return 0, 0, 0, err
			}
		}

		q := `INSERT INTO trips(mode, mode_raw, from_at, to_at, from_local, to_local,
			local_date, local_month, tz, duration_s, duration_declared_s,
			from_place_id, to_place_id, from_name_raw, to_name_raw,
			from_place_origin, to_place_origin,
			from_lat, from_lon, from_lat_gcj, from_lon_gcj,
			to_lat, to_lon, to_lat_gcj, to_lon_gcj,
			dedup_key, import_batch_id)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(dedup_key) DO NOTHING`
		if opt.DupStrategy == "overwrite" {
			q = strings.Replace(q, "DO NOTHING",
				`DO UPDATE SET duration_declared_s=excluded.duration_declared_s,
				 mode=excluded.mode`, 1)
		}
		res, err := tx.ExecContext(ctx, q,
			t.Mode, t.ModeRaw, t.FromAt, t.ToAt, t.FromLocal, t.ToLocal,
			t.LocalDate, t.LocalMonth, t.TZ, t.DurationS, t.DurationDeclaredS,
			fp, tp, t.FromNameRaw, t.ToNameRaw, t.FromOrigin, t.ToOrigin,
			fl, flo, flg, flog, tl, tlo, tlg, tlog,
			t.DedupKey, importID)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("写入交通记录失败（第 %d 行）: %w", t.RowNo, err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			skipped++
			continue
		}
		inserted++
	}
	return inserted, skipped, failed, nil
}

func (s *Service) commitWeather(ctx context.Context, tx *sql.Tx, rows []stagedWeather,
	placeIDs map[string]int64, importID int64, opt Options, d Decisions) (int, int, int, error) {
	var inserted, skipped, failed int
	for _, w := range rows {
		pid := placeIDs[w.PlaceKey]
		if pid == 0 {
			failed++
			continue
		}
		// reject 策略：命中即整批失败，交还给调用方回滚。
		if opt.WeatherDupStrategy == "reject" {
			var n int
			if err := tx.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM weather WHERE place_id=? AND hour_ts=?`, pid, w.HourTS).Scan(&n); err != nil {
				return 0, 0, 0, err
			}
			if n > 0 {
				return 0, 0, 0, fmt.Errorf(
					"天气「%s %s」已存在，按拒绝策略中止整批导入", w.PlaceNameRaw, w.HourLocal)
			}
		}
		// 逐组裁决优先于全局策略：用户显式选过「采用新值」的组要走 UPDATE。
		overwrite := opt.WeatherDupStrategy == "overwrite" ||
			(opt.WeatherDupStrategy != "reject" && d.WeatherOverrides[w.DedupKey] == "new")

		q := `INSERT INTO weather(place_id, place_name_raw, hour_ts, hour_local,
			local_date, local_month, condition, temp_c, feels_like_c, dew_point_c,
			humidity, precip_type, precip_mm, precip_prob, uv_index, uv_level,
			wind_dir, wind_speed_ms, visibility_km, import_batch_id, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,unixepoch())
			ON CONFLICT(place_id, hour_ts) DO NOTHING`
		if overwrite {
			q = strings.Replace(q, "DO NOTHING", `DO UPDATE SET
				condition=excluded.condition, temp_c=excluded.temp_c,
				feels_like_c=excluded.feels_like_c, dew_point_c=excluded.dew_point_c,
				humidity=excluded.humidity, precip_type=excluded.precip_type,
				precip_mm=excluded.precip_mm, precip_prob=excluded.precip_prob,
				uv_index=excluded.uv_index, uv_level=excluded.uv_level,
				wind_dir=excluded.wind_dir, wind_speed_ms=excluded.wind_speed_ms,
				visibility_km=excluded.visibility_km, updated_at=unixepoch(),
				import_batch_id=excluded.import_batch_id`, 1)
		}
		res, err := tx.ExecContext(ctx, q,
			pid, w.PlaceNameRaw, w.HourTS, w.HourLocal, w.LocalDate, w.LocalMonth,
			w.Condition, w.TempC, w.FeelsLikeC, w.DewPointC, w.Humidity,
			w.PrecipType, w.PrecipMM, w.PrecipProb, w.UVIndex, w.UVLevel,
			w.WindDir, w.WindSpeedMS, w.VisibilityKm, importID)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("写入天气失败（第 %d 行）: %w", w.RowNo, err)
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			skipped++
			continue
		}
		inserted++
	}
	return inserted, skipped, failed, nil
}

// backfillPlaceStats 回填地点的到访次数与首末到访时刻。
// 只在提交末尾跑一次，避免逐行维护计数器。
func (s *Service) backfillPlaceStats(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE places SET
			visit_count = COALESCE((
				SELECT COUNT(*) FROM visits v WHERE v.place_id = places.id), 0),
			first_visit_at = (
				SELECT MIN(v.arrived_at) FROM visits v WHERE v.place_id = places.id),
			last_visit_at = (
				SELECT MAX(v.arrived_at) FROM visits v WHERE v.place_id = places.id)`)
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
