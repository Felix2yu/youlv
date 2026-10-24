package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound 是查不到记录时的哨兵错误。
var ErrNotFound = errors.New("记录不存在")

// VisitDetail 是一次到访的完整档案。
//
// 与时间轴用的 VisitDTO 分开：DTO 是列表里的精简视图，这里是详情视图，
// 多出「为什么是这条记录」的信息——解析依据、原始坐标、所属批次。
// 这些字段平时用不上，但排查「为什么这条到访绑到了这个地点」时全靠它们。
type VisitDetail struct {
	ID            int64  `json:"id"`
	PlaceID       int64  `json:"place_id"`
	PlaceName     string `json:"place_name"`
	PlaceNameRaw  string `json:"place_name_raw"`
	DisplayName   string `json:"display_name"`
	City          string `json:"city,omitempty"`
	AdminArea     string `json:"admin_area,omitempty"`
	District      string `json:"district,omitempty"`
	Activity      string `json:"activity"`
	ActivityRaw   string `json:"activity_raw"`
	Color         string `json:"color"`
	ArrivedAt     int64  `json:"arrived_at"`
	DepartedAt    *int64 `json:"departed_at,omitempty"`
	DurationS     *int64 `json:"duration_s,omitempty"`
	TZ            string `json:"tz"`
	ArrivedLocal  string `json:"arrived_local"`
	DepartedLocal string `json:"departed_local,omitempty"`
	LocalDate     string `json:"local_date"`
	LocalMonth    string `json:"local_month"`
	LocalYear     int    `json:"local_year"`
	LocalHour     int    `json:"local_hour"`
	LocalDow      int    `json:"local_dow"`
	// Lat/Lon 是主坐标，取决于 frame 参数；下面两组是固定的两套坐标。
	Lat               *float64 `json:"lat,omitempty"`
	Lon               *float64 `json:"lon,omitempty"`
	LatGCJ            *float64 `json:"lat_gcj,omitempty"`
	LonGCJ            *float64 `json:"lon_gcj,omitempty"`
	LatRaw            *float64 `json:"lat_raw,omitempty"`
	LonRaw            *float64 `json:"lon_raw,omitempty"`
	CoordFrame        string   `json:"coord_frame"`
	ResolveMethod     string   `json:"resolve_method"`
	ResolveDistM      *float64 `json:"resolve_dist_m,omitempty"`
	ResolveConfidence string   `json:"resolve_confidence"`
	Note              string   `json:"note,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	ImportBatchID     *int64   `json:"import_batch_id,omitempty"`
	ImportBatchNo     string   `json:"import_batch_no,omitempty"`
}

// Visit 返回一次到访的详情。
//
// frame 决定 Lat/Lon 取哪一套坐标（wgs84 或 gcj02），两套副本始终都返回。
func (s *Store) Visit(ctx context.Context, id int64, frame string) (*VisitDetail, error) {
	latE, lonE := coordExpr(frame)
	q := fmt.Sprintf(`
		SELECT v.id, v.place_id, COALESCE(p.display_name, v.place_name_raw), v.place_name_raw,
		       COALESCE(p.display_name,''), COALESCE(p.city,''), COALESCE(p.admin_area,''),
		       COALESCE(p.district,''),
		       COALESCE(a.name, v.activity_raw), COALESCE(v.activity_raw,''),
		       COALESCE(a.color,'%s'),
		       v.arrived_at, v.departed_at, v.duration_s, v.tz, v.arrived_local,
		       COALESCE(v.departed_local,''), v.local_date, v.local_month, v.local_year,
		       v.local_hour, v.local_dow,
		       %s, %s, v.lat_gcj, v.lon_gcj, v.lat_raw, v.lon_raw,
		       v.coord_frame, COALESCE(v.resolve_method,''), v.resolve_dist_m,
		       COALESCE(v.resolve_confidence,''), COALESCE(v.note,''),
		       v.import_batch_id, COALESCE(i.batch_no,'')
		FROM visits v
		LEFT JOIN places p ON p.id = v.place_id
		LEFT JOIN activities a ON a.id = v.activity_id
		LEFT JOIN imports i ON i.id = v.import_batch_id
		WHERE v.id = ?`, DefaultActivityColor, latE, lonE)

	var d VisitDetail
	err := s.DB.QueryRowContext(ctx, q, id).Scan(
		&d.ID, &d.PlaceID, &d.PlaceName, &d.PlaceNameRaw, &d.DisplayName,
		&d.City, &d.AdminArea, &d.District,
		&d.Activity, &d.ActivityRaw, &d.Color,
		&d.ArrivedAt, &d.DepartedAt, &d.DurationS, &d.TZ, &d.ArrivedLocal,
		&d.DepartedLocal, &d.LocalDate, &d.LocalMonth, &d.LocalYear,
		&d.LocalHour, &d.LocalDow,
		&d.Lat, &d.Lon, &d.LatGCJ, &d.LonGCJ, &d.LatRaw, &d.LonRaw,
		&d.CoordFrame, &d.ResolveMethod, &d.ResolveDistM,
		&d.ResolveConfidence, &d.Note,
		&d.ImportBatchID, &d.ImportBatchNo)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	tags, err := s.visitTags(ctx, id)
	if err != nil {
		return nil, err
	}
	d.Tags = tags
	return &d, nil
}

// visitTags 取一次到访的标签（权威来源是 visit_tags 表，不是 tags_raw 串）。
//
// tags_raw 是 CSV 里的原始文本，只在关联表缺失时作为兜底。
func (s *Store) visitTags(ctx context.Context, visitID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT t.name FROM visit_tags vt JOIN tags t ON t.id = vt.tag_id
		WHERE vt.visit_id = ? ORDER BY t.name`, visitID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
