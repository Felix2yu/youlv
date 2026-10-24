// Package store 承载全部读侧查询：时间轴、地点、统计、地图、天气。
//
// 写侧全在 internal/importer 里（导入管线），这里只管读，两边不互相调用。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Store 是读侧入口。
type Store struct {
	DB *sql.DB
	TZ *time.Location
}

func New(db *sql.DB, tz *time.Location) *Store { return &Store{DB: db, TZ: tz} }

// ---------- 数据概览 ----------

// Meta 是首屏需要的全局信息。
type Meta struct {
	HasData    bool   `json:"has_data"`
	Visits     int    `json:"visits"`
	Places     int    `json:"places"`
	Trips      int    `json:"trips"`
	Weather    int    `json:"weather"`
	Cities     int    `json:"cities"`
	Activities int    `json:"activities"`
	FirstDate  string `json:"first_date"`
	LastDate   string `json:"last_date"`
	SchemaVer  int    `json:"schema_version"`
}

func (s *Store) Meta(ctx context.Context) (*Meta, error) {
	m := &Meta{}
	err := s.DB.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM visits),
		       (SELECT COUNT(*) FROM places),
		       (SELECT COUNT(*) FROM trips),
		       (SELECT COUNT(*) FROM weather),
		       (SELECT COUNT(DISTINCT city) FROM places WHERE city IS NOT NULL),
		       (SELECT COUNT(*) FROM activities),
		       COALESCE((SELECT MIN(local_date) FROM visits), ''),
		       COALESCE((SELECT MAX(local_date) FROM visits), ''),
		       COALESCE((SELECT MAX(version) FROM schema_version), 0)`).Scan(
		&m.Visits, &m.Places, &m.Trips, &m.Weather,
		&m.Cities, &m.Activities, &m.FirstDate, &m.LastDate, &m.SchemaVer)
	if err != nil {
		return nil, err
	}
	m.HasData = m.Visits > 0
	return m, nil
}

// ---------- 时间轴 ----------

// VisitDTO 是时间轴里的一条到访。
type VisitDTO struct {
	ID            int64    `json:"id"`
	PlaceID       int64    `json:"place_id"`
	PlaceName     string   `json:"place_name"`
	City          string   `json:"city,omitempty"`
	Activity      string   `json:"activity"`
	Color         string   `json:"color"`
	ArrivedAt     int64    `json:"arrived_at"`
	DepartedAt    *int64   `json:"departed_at,omitempty"`
	DurationS     *int64   `json:"duration_s,omitempty"`
	ArrivedLocal  string   `json:"arrived_local"`
	DepartedLocal string   `json:"departed_local,omitempty"`
	LocalDate     string   `json:"local_date"`
	Lat           *float64 `json:"lat,omitempty"`
	Lon           *float64 `json:"lon,omitempty"`
	LatGCJ        *float64 `json:"lat_gcj,omitempty"`
	LonGCJ        *float64 `json:"lon_gcj,omitempty"`
	Note          string   `json:"note,omitempty"`
	Tags          []string `json:"tags,omitempty"`
	TempC         *float64 `json:"temp_c,omitempty"`
	Condition     string   `json:"condition,omitempty"`
	WeatherLevel  string   `json:"weather_level,omitempty"`
}

// coordExpr 按坐标系返回取哪一列。
// 两个坐标系各存一份，前端不自己做浮点变换——变换只在服务端做一次。
func coordExpr(frame string) (lat, lon string) {
	if frame == "gcj02" {
		return "COALESCE(v.lat_gcj, v.lat)", "COALESCE(v.lon_gcj, v.lon)"
	}
	return "v.lat", "v.lon"
}

func (s *Store) TimelineDay(ctx context.Context, date, frame string) ([]VisitDTO, error) {
	latE, lonE := coordExpr(frame)
	q := fmt.Sprintf(`
		SELECT v.id, v.place_id, COALESCE(p.display_name, v.place_name_raw),
		       COALESCE(p.city,''), COALESCE(a.name, v.activity_raw), COALESCE(a.color,'#8E8E93'),
		       v.arrived_at, v.departed_at, v.duration_s, v.arrived_local,
		       COALESCE(v.departed_local,''), v.local_date, %s, %s,
		       COALESCE(v.note,''), COALESCE(v.tags_raw,'')
		FROM visits v
		LEFT JOIN places p ON p.id = v.place_id
		LEFT JOIN activities a ON a.id = v.activity_id
		WHERE v.local_date = ?
		ORDER BY v.arrived_at`, latE, lonE)
	rows, err := s.DB.QueryContext(ctx, q, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanVisits(rows)
}

// DayBucket 是月度日历里的一天。
type DayBucket struct {
	Date   string `json:"date"`
	Count  int    `json:"count"`
	DurS   int64  `json:"duration_s"`
	Places int    `json:"places"`
}

func (s *Store) TimelineMonth(ctx context.Context, month string) ([]DayBucket, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT local_date, COUNT(*), COALESCE(SUM(duration_s),0), COUNT(DISTINCT place_id)
		FROM visits WHERE local_month = ? GROUP BY local_date ORDER BY local_date`, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayBucket
	for rows.Next() {
		var b DayBucket
		if err := rows.Scan(&b.Date, &b.Count, &b.DurS, &b.Places); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// TimelineRange 按条件区间查询，支持活动/地点/关键词过滤。
func (s *Store) TimelineRange(ctx context.Context, f TimelineFilter, frame string) ([]VisitDTO, int, error) {
	latE, lonE := coordExpr(frame)
	var where []string
	var args []any
	if f.From != "" {
		where = append(where, "v.local_date >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "v.local_date <= ?")
		args = append(args, f.To)
	}
	if f.Activity != "" {
		where = append(where, "COALESCE(a.name, v.activity_raw) = ?")
		args = append(args, f.Activity)
	}
	if f.PlaceID > 0 {
		where = append(where, "v.place_id = ?")
		args = append(args, f.PlaceID)
	}
	if f.Q != "" {
		where = append(where, "(p.display_name LIKE ? OR v.place_name_raw LIKE ? OR v.note LIKE ?)")
		like := "%" + f.Q + "%"
		args = append(args, like, like, like)
	}
	w := ""
	if len(where) > 0 {
		w = "WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM visits v
		 LEFT JOIN places p ON p.id = v.place_id
		 LEFT JOIN activities a ON a.id = v.activity_id `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	limit, offset := f.Limit, f.Offset
	if limit <= 0 {
		limit = 100
	}
	q := fmt.Sprintf(`
		SELECT v.id, v.place_id, COALESCE(p.display_name, v.place_name_raw),
		       COALESCE(p.city,''), COALESCE(a.name, v.activity_raw), COALESCE(a.color,'#8E8E93'),
		       v.arrived_at, v.departed_at, v.duration_s, v.arrived_local,
		       COALESCE(v.departed_local,''), v.local_date, %s, %s,
		       COALESCE(v.note,''), COALESCE(v.tags_raw,'')
		FROM visits v
		LEFT JOIN places p ON p.id = v.place_id
		LEFT JOIN activities a ON a.id = v.activity_id
		%s ORDER BY v.arrived_at DESC LIMIT ? OFFSET ?`, latE, lonE, w)
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list, err := scanVisits(rows)
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// TimelineFilter 是时间轴区间查询的条件。
type TimelineFilter struct {
	From, To, Q, Activity string
	PlaceID               int64
	Limit, Offset         int
}

func scanVisits(rows *sql.Rows) ([]VisitDTO, error) {
	var out []VisitDTO
	for rows.Next() {
		var v VisitDTO
		var tagsRaw string
		if err := rows.Scan(&v.ID, &v.PlaceID, &v.PlaceName, &v.City, &v.Activity, &v.Color,
			&v.ArrivedAt, &v.DepartedAt, &v.DurationS, &v.ArrivedLocal, &v.DepartedLocal,
			&v.LocalDate, &v.Lat, &v.Lon, &v.Note, &tagsRaw); err != nil {
			return nil, err
		}
		if tagsRaw != "" {
			for _, t := range strings.Split(tagsRaw, ",") {
				if t = strings.TrimSpace(t); t != "" {
					v.Tags = append(v.Tags, t)
				}
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---------- 地点 ----------

type PlaceDTO struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name"`
	Lat         *float64 `json:"lat,omitempty"`
	Lon         *float64 `json:"lon,omitempty"`
	LatGCJ      *float64 `json:"lat_gcj,omitempty"`
	LonGCJ      *float64 `json:"lon_gcj,omitempty"`
	City        string   `json:"city,omitempty"`
	AdminArea   string   `json:"admin_area,omitempty"`
	District    string   `json:"district,omitempty"`
	Street      string   `json:"street,omitempty"`
	Origin      string   `json:"origin"`
	IsStub      bool     `json:"is_stub"`
	VisitCount  int      `json:"visit_count"`
	FirstAt     *int64   `json:"first_visit_at,omitempty"`
	LastAt      *int64   `json:"last_visit_at,omitempty"`
}

func (s *Store) Places(ctx context.Context, f PlaceFilter) ([]PlaceDTO, int, error) {
	var where []string
	var args []any
	if f.Q != "" {
		where = append(where, "(name LIKE ? OR display_name LIKE ? OR city LIKE ?)")
		like := "%" + f.Q + "%"
		args = append(args, like, like, like)
	}
	if f.City != "" {
		where = append(where, "city = ?")
		args = append(args, f.City)
	}
	if f.Origin != "" {
		where = append(where, "origin = ?")
		args = append(args, f.Origin)
	}
	if f.HasCoord {
		where = append(where, "lat IS NOT NULL")
	}
	w := ""
	if len(where) > 0 {
		w = "WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM places `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	order := "visit_count DESC, name"
	switch f.Sort {
	case "name":
		order = "name"
	case "recent":
		order = "COALESCE(last_visit_at,0) DESC, name"
	}
	args = append(args, limit, f.Offset)
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, name, display_name, lat, lon, lat_gcj, lon_gcj,
		       COALESCE(city,''), COALESCE(admin_area,''), COALESCE(district,''),
		       COALESCE(street,''), origin, is_stub, visit_count, first_visit_at, last_visit_at
		FROM places %s ORDER BY %s LIMIT ? OFFSET ?`, w, order), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []PlaceDTO
	for rows.Next() {
		var p PlaceDTO
		var stub int
		if err := rows.Scan(&p.ID, &p.Name, &p.DisplayName, &p.Lat, &p.Lon, &p.LatGCJ, &p.LonGCJ,
			&p.City, &p.AdminArea, &p.District, &p.Street, &p.Origin, &stub,
			&p.VisitCount, &p.FirstAt, &p.LastAt); err != nil {
			return nil, 0, err
		}
		p.IsStub = stub == 1
		out = append(out, p)
	}
	return out, total, rows.Err()
}

type PlaceFilter struct {
	Q, City, Origin, Sort string
	HasCoord              bool
	Limit, Offset         int
}

// ---------- 统计 ----------

type ActivityStat struct {
	Name  string `json:"name"`
	Color string `json:"color"`
	Count int    `json:"count"`
	DurS  int64  `json:"duration_s"`
}

func (s *Store) StatsActivities(ctx context.Context, from, to string) ([]ActivityStat, error) {
	var where []string
	var args []any
	if from != "" {
		where = append(where, "v.local_date >= ?")
		args = append(args, from)
	}
	if to != "" {
		where = append(where, "v.local_date <= ?")
		args = append(args, to)
	}
	w := ""
	if len(where) > 0 {
		w = "WHERE " + strings.Join(where, " AND ")
	}
	rows, err := s.DB.QueryContext(ctx, fmt.Sprintf(`
		SELECT COALESCE(a.name, v.activity_raw), COALESCE(a.color,'#8E8E93'),
		       COUNT(*), COALESCE(SUM(v.duration_s),0)
		FROM visits v LEFT JOIN activities a ON a.id = v.activity_id
		%s GROUP BY 1, 2 ORDER BY 4 DESC`, w), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActivityStat
	for rows.Next() {
		var a ActivityStat
		if err := rows.Scan(&a.Name, &a.Color, &a.Count, &a.DurS); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

type CityStat struct {
	City     string `json:"city"`
	Province string `json:"province"`
	Places   int    `json:"places"`
	Visits   int    `json:"visits"`
	DurS     int64  `json:"duration_s"`
	FirstAt  *int64 `json:"first_visit_at,omitempty"`
}

// StatsCities 是「城市护照」：按城市聚合到访。
func (s *Store) StatsCities(ctx context.Context) ([]CityStat, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT COALESCE(p.city,'未标注'), COALESCE(p.admin_area,''),
		       COUNT(DISTINCT p.id), COUNT(*), COALESCE(SUM(v.duration_s),0), MIN(v.arrived_at)
		FROM visits v JOIN places p ON p.id = v.place_id
		GROUP BY p.city, p.admin_area ORDER BY 5 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CityStat
	for rows.Next() {
		var c CityStat
		if err := rows.Scan(&c.City, &c.Province, &c.Places, &c.Visits, &c.DurS, &c.FirstAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

type ModeStat struct {
	Mode  string `json:"mode"`
	Count int    `json:"count"`
	DurS  int64  `json:"duration_s"`
}

func (s *Store) StatsCommute(ctx context.Context) ([]ModeStat, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT mode, COUNT(*), COALESCE(SUM(duration_s),0)
		FROM trips GROUP BY mode ORDER BY 3 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModeStat
	for rows.Next() {
		var m ModeStat
		if err := rows.Scan(&m.Mode, &m.Count, &m.DurS); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type HourStat struct {
	Hour int `json:"hour"`
	Dow  int `json:"dow"`
	N    int `json:"n"`
}

func (s *Store) StatsHourly(ctx context.Context) ([]HourStat, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT local_hour, local_dow, COUNT(*) FROM visits GROUP BY local_hour, local_dow`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HourStat
	for rows.Next() {
		var h HourStat
		if err := rows.Scan(&h.Hour, &h.Dow, &h.N); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---------- 地图 ----------

// MapPoint 是地图上的一个点位。
type MapPoint struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	LatGCJ  float64 `json:"lat_gcj"`
	LonGCJ  float64 `json:"lon_gcj"`
	N       int     `json:"n"`
	DurS    int64   `json:"duration_s"`
	Name    string  `json:"name"`
	PlaceID int64   `json:"place_id"`
}

// MapPoints 返回 bbox 内按地点聚合的点位。
func (s *Store) MapPoints(ctx context.Context, bbox BBox) ([]MapPoint, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT COALESCE(v.lat, p.lat), COALESCE(v.lon, p.lon),
		       COALESCE(v.lat_gcj, p.lat_gcj), COALESCE(v.lon_gcj, p.lon_gcj),
		       COUNT(*), COALESCE(SUM(v.duration_s),0),
		       COALESCE(p.display_name, v.place_name_raw), COALESCE(v.place_id,0)
		FROM visits v LEFT JOIN places p ON p.id = v.place_id
		WHERE COALESCE(v.lat, p.lat) BETWEEN ? AND ?
		  AND COALESCE(v.lon, p.lon) BETWEEN ? AND ?
		GROUP BY COALESCE(v.place_id,0)
		ORDER BY 5 DESC`, bbox.South, bbox.North, bbox.West, bbox.East)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MapPoint
	for rows.Next() {
		var m MapPoint
		if err := rows.Scan(&m.Lat, &m.Lon, &m.LatGCJ, &m.LonGCJ, &m.N, &m.DurS, &m.Name, &m.PlaceID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// BBox 是经纬度范围：西、南、东、北。
//
// json tag 不能省：这个结构体直接经 /api/map/bounds 序列化给前端，
// 漏了 tag 就会输出 West/South 这种 Go 风格大写键，前端读到的是
// undefined，四个 undefined 相互比较还恰好“相等”，表现为
// 地图页永远显示「还没有带坐标的到访」—— 单元测试查不出来，
// 因为它不经过 JSON。
type BBox struct {
	West  float64 `json:"west"`
	South float64 `json:"south"`
	East  float64 `json:"east"`
	North float64 `json:"north"`
}

// Bounds 返回全量数据的经纬度范围，供地图初始化视野。
func (s *Store) Bounds(ctx context.Context) (*BBox, error) {
	var b BBox
	err := s.DB.QueryRowContext(ctx, `
		SELECT COALESCE(MIN(lon),0), COALESCE(MIN(lat),0),
		       COALESCE(MAX(lon),0), COALESCE(MAX(lat),0)
		FROM visits WHERE lat IS NOT NULL`).Scan(&b.West, &b.South, &b.East, &b.North)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ---------- 天气 ----------

// WeatherDTO 是一条天气记录，带来源等级供 UI 标「近似」。
type WeatherDTO struct {
	PlaceName string   `json:"place_name"`
	HourLocal string   `json:"hour_local"`
	Condition string   `json:"condition,omitempty"`
	TempC     *float64 `json:"temp_c,omitempty"`
	FeelsC    *float64 `json:"feels_like_c,omitempty"`
	Humidity  *float64 `json:"humidity,omitempty"`
	WindDir   string   `json:"wind_dir,omitempty"`
	WindMS    *float64 `json:"wind_speed_ms,omitempty"`
	UVIndex   *int     `json:"uv_index,omitempty"`
	UVLevel   string   `json:"uv_level,omitempty"`
	PrecipMM  *float64 `json:"precip_mm,omitempty"`
	VisKm     *float64 `json:"visibility_km,omitempty"`
	Level     string   `json:"level"` // exact|nearby_hour|nearby_place|none

	hourTS int64 // 内部用：判断命中的是不是精确整点
}

// weatherCols 是天气查询的固定列清单。
//
// 刻意不写 SELECT w.*：表一旦加列（比如未来的 superseded_at），w.* 的列序
// 会跟着变，而 Scan 是按位置取值的——那会静默把数据读到错误的字段上，
// 不报错、不告警，只在很久以后才被人发现温度显示成了湿度。
const weatherCols = `w.place_name_raw, w.hour_ts, w.hour_local, w.condition,
	w.temp_c, w.feels_like_c, w.humidity, w.precip_mm,
	w.uv_index, w.uv_level, w.wind_dir, w.wind_speed_ms, w.visibility_km`

// WeatherFor 三级降级查询：精确 → 邻近小时 → 同城最近点。
//
// 113 个到访地点没有天气数据，直接左连接会是 NULL；降级让这些地点也能拿到
// 一个「近似」值，并由 UI 明确标注，而不是假装精确。
func (s *Store) WeatherFor(ctx context.Context, placeID, at int64, approx bool) (*WeatherDTO, error) {
	hour := at - at%3600
	w, err := s.queryWeather(ctx, `
		SELECT `+weatherCols+` FROM weather w
		WHERE w.place_id = ? AND w.hour_ts <= ? AND w.hour_ts >= ? - 3600
		ORDER BY ABS(w.hour_ts - ?) LIMIT 1`, placeID, hour, hour, hour)
	if err != nil {
		return nil, err
	}
	if w != nil {
		w.Level = "exact"
		if w.HourLocal == "" || w.hourTS != hour {
			w.Level = "nearby_hour"
		}
		return w, nil
	}
	if !approx || placeID == 0 {
		return &WeatherDTO{Level: "none"}, nil
	}
	// 同城最近点：50 km 内的同小时记录。
	w, err = s.queryWeather(ctx, `
		SELECT `+weatherCols+` FROM weather w
		JOIN places p ON p.id = w.place_id
		JOIN places me ON me.id = ?
		WHERE w.hour_ts = ? AND p.city = me.city AND p.id != me.id
		  AND p.lat IS NOT NULL AND me.lat IS NOT NULL
		  AND ABS(p.lat - me.lat) < 0.5 AND ABS(p.lon - me.lon) < 0.5
		ORDER BY (p.lat-me.lat)*(p.lat-me.lat) + (p.lon-me.lon)*(p.lon-me.lon)
		LIMIT 1`, placeID, hour)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return &WeatherDTO{Level: "none"}, nil
	}
	w.Level = "nearby_place"
	return w, nil
}

func (s *Store) queryWeather(ctx context.Context, q string, args ...any) (*WeatherDTO, error) {
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var w WeatherDTO
	var hourTS int64
	var cond, wind, uvl *string
	var uv *int
	err = rows.Scan(&w.PlaceName, &hourTS, &w.HourLocal, &cond, &w.TempC, &w.FeelsC,
		&w.Humidity, &w.PrecipMM, &uv, &uvl, &wind, &w.WindMS, &w.VisKm)
	if err != nil {
		return nil, err
	}
	w.Condition = deref(cond)
	w.WindDir = deref(wind)
	w.UVLevel = deref(uvl)
	w.UVIndex = uv
	w.hourTS = hourTS
	return &w, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
