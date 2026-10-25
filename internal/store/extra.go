package store

import (
	"context"
	"fmt"
	"strings"
)

// 本文件是「读侧的补充查询」：地点详情、地点的到访、交通段、导出。
// 它们都是既有数据模型早就支持、但之前没有出口的读法。

// ---------- 地点详情 ----------

// PlaceDetail 是一个地点加上它的到访概况。
type PlaceDetail struct {
	PlaceDTO
	// Recent 是最近几次到访（不含分页，只给个概览）。
	Recent []VisitDTO `json:"recent"`
	// DurS 是累计停留秒数。
	DurS int64 `json:"duration_s"`
}

// PlaceDetail 取单个地点。找不到返回 nil。
func (s *Store) PlaceDetail(ctx context.Context, id int64, frame string) (*PlaceDetail, error) {
	var d PlaceDetail
	var lat, lon, latGCJ, lonGCJ *float64
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, name, COALESCE(display_name,''), lat, lon, lat_gcj, lon_gcj,
		       COALESCE(city,''), COALESCE(admin_area,''), COALESCE(district,''),
		       COALESCE(street,''), origin, is_stub, visit_count,
		       first_visit_at, last_visit_at
		FROM places WHERE id=?`, id).
		Scan(&d.ID, &d.Name, &d.DisplayName, &lat, &lon, &latGCJ, &lonGCJ,
			&d.City, &d.AdminArea, &d.District, &d.Street, &d.Origin, &d.IsStub,
			&d.VisitCount, &d.FirstAt, &d.LastAt)
	if err != nil {
		return nil, err
	}
	d.Lat, d.Lon, d.LatGCJ, d.LonGCJ = lat, lon, latGCJ, lonGCJ

	_ = s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(duration_s),0) FROM visits WHERE place_id=?`, id).Scan(&d.DurS)

	visits, err := s.PlaceVisits(ctx, id, 20, 0, frame)
	if err != nil {
		return nil, err
	}
	d.Recent = visits
	return &d, nil
}

// PlaceVisits 列出某个地点的到访，按时间倒序。
func (s *Store) PlaceVisits(ctx context.Context, id int64, limit, offset int, frame string) ([]VisitDTO, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	latE, lonE := coordExpr(frame)
	q := fmt.Sprintf(`
		SELECT v.id, v.place_id, COALESCE(p.display_name, v.place_name_raw),
		       COALESCE(p.city,''), COALESCE(a.name, v.activity_raw), COALESCE(a.color,'#8E8E93'),
		       v.arrived_at, v.departed_at, v.duration_s, v.arrived_local,
		       COALESCE(v.departed_local,''), v.local_date, %s, %s, %s,
		       COALESCE(v.note,''), COALESCE(v.tags_raw,'')
		FROM visits v
		LEFT JOIN places p ON p.id = v.place_id
		LEFT JOIN activities a ON a.id = v.activity_id
		WHERE v.place_id = ?
		ORDER BY v.arrived_at DESC LIMIT ? OFFSET ?`, latE, lonE, gcjExpr)
	rows, err := s.DB.QueryContext(ctx, q, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanVisits(rows)
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []VisitDTO{}
	}
	return out, nil
}

// ---------- 交通段 ----------

// TripDTO 是一段交通。地点名在查库时解析，缺失时退回原始文本。
type TripDTO struct {
	ID          int64    `json:"id"`
	Mode        string   `json:"mode"`
	ModeRaw     string   `json:"mode_raw"`
	FromAt      int64    `json:"from_at"`
	ToAt        int64    `json:"to_at"`
	FromLocal   string   `json:"from_local"`
	ToLocal     string   `json:"to_local"`
	LocalDate   string   `json:"local_date"`
	DurationS   int64    `json:"duration_s"`
	FromPlace   string   `json:"from_place"`
	ToPlace     string   `json:"to_place"`
	FromLat     *float64 `json:"from_lat,omitempty"`
	FromLon     *float64 `json:"from_lon,omitempty"`
	ToLat       *float64 `json:"to_lat,omitempty"`
	ToLon       *float64 `json:"to_lon,omitempty"`
	FromPlaceID int64    `json:"from_place_id"`
	ToPlaceID   int64    `json:"to_place_id"`
}

// TripFilter 是交通段的筛选条件。
type TripFilter struct {
	From, To string // 本地日期，闭区间
	Mode     string
	Limit    int
	Offset   int
}

// Trips 列出交通段，按出发时间倒序。
func (s *Store) Trips(ctx context.Context, f TripFilter) ([]TripDTO, int, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	var where []string
	var args []any
	if f.From != "" {
		where = append(where, "t.local_date >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "t.local_date <= ?")
		args = append(args, f.To)
	}
	if f.Mode != "" {
		where = append(where, "t.mode = ?")
		args = append(args, f.Mode)
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}

	var total int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM trips t`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := fmt.Sprintf(`
		SELECT t.id, t.mode, t.mode_raw, t.from_at, t.to_at, t.from_local, t.to_local,
		       t.local_date, t.duration_s,
		       COALESCE(pf.display_name, t.from_name_raw),
		       COALESCE(pt.display_name, t.to_name_raw),
		       COALESCE(t.from_lat, pf.lat), COALESCE(t.from_lon, pf.lon),
		       COALESCE(t.to_lat, pt.lat), COALESCE(t.to_lon, pt.lon),
		       COALESCE(t.from_place_id,0), COALESCE(t.to_place_id,0)
		FROM trips t
		LEFT JOIN places pf ON pf.id = t.from_place_id
		LEFT JOIN places pt ON pt.id = t.to_place_id
		%s ORDER BY t.from_at DESC LIMIT ? OFFSET ?`, w)
	rows, err := s.DB.QueryContext(ctx, q, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []TripDTO{}
	for rows.Next() {
		var t TripDTO
		if err := rows.Scan(&t.ID, &t.Mode, &t.ModeRaw, &t.FromAt, &t.ToAt,
			&t.FromLocal, &t.ToLocal, &t.LocalDate, &t.DurationS,
			&t.FromPlace, &t.ToPlace, &t.FromLat, &t.FromLon,
			&t.ToLat, &t.ToLon, &t.FromPlaceID, &t.ToPlaceID); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

// ---------- 导出 ----------

// ExportRow 是导出用的扁平行：两个坐标系都给，省得用户再自己换算。
type ExportRow struct {
	LocalDate  string
	ArrivedAt  int64
	DepartedAt *int64
	DurationS  *int64
	Place      string
	City       string
	Activity   string
	Condition  string
	TempC      *float64
	Tags       string
	Note       string
	Lat        *float64
	Lon        *float64
	LatGCJ     *float64
	LonGCJ     *float64
}

// ExportVisits 按条件取出到访用于导出。dateFrom/dateTo 为空表示不限。
func (s *Store) ExportVisits(ctx context.Context, dateFrom, dateTo string, limit int) ([]ExportRow, error) {
	if limit <= 0 || limit > 100000 {
		limit = 50000
	}
	var where []string
	var args []any
	if dateFrom != "" {
		where = append(where, "v.local_date >= ?")
		args = append(args, dateFrom)
	}
	if dateTo != "" {
		where = append(where, "v.local_date <= ?")
		args = append(args, dateTo)
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	q := fmt.Sprintf(`
		SELECT v.local_date, v.arrived_at, v.departed_at, v.duration_s,
		       COALESCE(p.display_name, v.place_name_raw), COALESCE(p.city,''),
		       COALESCE(a.name, v.activity_raw),
		       COALESCE(w.condition,''), w.temp_c,
		       COALESCE(v.tags_raw,''), COALESCE(v.note,''),
		       v.lat, v.lon, v.lat_gcj, v.lon_gcj
		FROM visits v
		LEFT JOIN places p ON p.id = v.place_id
		LEFT JOIN activities a ON a.id = v.activity_id
		LEFT JOIN weather w ON w.place_id = v.place_id
		     AND w.hour_ts = (v.arrived_at - v.arrived_at %% 3600)
		%s ORDER BY v.arrived_at LIMIT ?`, w)
	rows, err := s.DB.QueryContext(ctx, q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportRow{}
	for rows.Next() {
		var r ExportRow
		if err := rows.Scan(&r.LocalDate, &r.ArrivedAt, &r.DepartedAt, &r.DurationS,
			&r.Place, &r.City, &r.Activity, &r.Condition, &r.TempC,
			&r.Tags, &r.Note, &r.Lat, &r.Lon, &r.LatGCJ, &r.LonGCJ); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
