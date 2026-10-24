package store

import (
	"context"
)

// PlaceStats 是地点库的概览。
//
// 最有价值的是 Stubs 与 WithoutCoord 两个数：它们直接回答「还有多少地点
// 只是被别人提到过、没有自己的坐标」——这些地点不出现在足迹地图上，
// 需要用户补一份地点记录才能升级成正式地点（导入时会自动升级）。
type PlaceStats struct {
	Total        int            `json:"total"`
	WithCoord    int            `json:"with_coord"`
	WithoutCoord int            `json:"without_coord"`
	Stubs        int            `json:"stubs"`
	ByOrigin     map[string]int `json:"by_origin"`
	Cities       int            `json:"cities"`
	TopCities    []CityStat     `json:"top_cities"`
}

// PlaceStats 汇总地点库。city / origin 非空时只统计对应子集。
func (s *Store) PlaceStats(ctx context.Context, city, origin string) (*PlaceStats, error) {
	st := &PlaceStats{ByOrigin: map[string]int{}}

	where := ""
	args := []any{}
	if city != "" || origin != "" {
		where = " WHERE "
		if city != "" {
			where += "city = ?"
			args = append(args, city)
		}
		if origin != "" {
			if where != " WHERE " {
				where += " AND "
			}
			where += "origin = ?"
			args = append(args, origin)
		}
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT COUNT(*),
		       SUM(CASE WHEN lat IS NOT NULL AND lon IS NOT NULL THEN 1 ELSE 0 END),
		       SUM(CASE WHEN lat IS NULL OR lon IS NULL THEN 1 ELSE 0 END),
		       SUM(CASE WHEN is_stub = 1 THEN 1 ELSE 0 END),
		       COUNT(DISTINCT NULLIF(city,'')),
		       origin
		FROM places`+where+` GROUP BY origin`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var total, withCoord, withoutCoord, stubs, cities int
		var o string
		if err := rows.Scan(&total, &withCoord, &withoutCoord, &stubs, &cities, &o); err != nil {
			return nil, err
		}
		st.Total += total
		st.WithCoord += withCoord
		st.WithoutCoord += withoutCoord
		st.Stubs += stubs
		st.ByOrigin[o] = total
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 城市数不能按 origin 分组求和——同一个城市会横跨多个 origin。
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT NULLIF(city,'')) FROM places`+where, args...).Scan(&st.Cities); err != nil {
		return nil, err
	}

	cities, err := s.StatsCities(ctx)
	if err != nil {
		return nil, err
	}
	if len(cities) > 10 {
		cities = cities[:10]
	}
	st.TopCities = cities
	return st, nil
}
