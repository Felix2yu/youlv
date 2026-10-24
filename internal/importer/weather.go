package importer

import (
	"context"
	"fmt"
)

// 天气字段的中文列名，用于冲突裁决表的表头。
var weatherFieldLabels = []struct {
	Field string
	Label string
}{
	{"condition", "天气状况"},
	{"temp_c", "温度 (°C)"},
	{"feels_like_c", "体感温度 (°C)"},
	{"dew_point_c", "露点 (°C)"},
	{"humidity", "湿度"},
	{"precip_type", "降水类型"},
	{"precip_mm", "降水量 (mm)"},
	{"precip_prob", "降水概率"},
	{"uv_index", "紫外线指数"},
	{"uv_level", "紫外线等级"},
	{"wind_dir", "风向"},
	{"wind_speed_ms", "风速 (m/s)"},
	{"visibility_km", "能见度 (km)"},
}

// weatherGet 按字段名取出已归一化的值，nil 表示无数据。
func weatherGet(w stagedWeather, field string) any {
	switch field {
	case "condition":
		return derefStr(w.Condition)
	case "temp_c":
		return derefF(w.TempC)
	case "feels_like_c":
		return derefF(w.FeelsLikeC)
	case "dew_point_c":
		return derefF(w.DewPointC)
	case "humidity":
		return derefF(w.Humidity)
	case "precip_type":
		return derefStr(w.PrecipType)
	case "precip_mm":
		return derefF(w.PrecipMM)
	case "precip_prob":
		return derefF(w.PrecipProb)
	case "uv_index":
		return derefI(w.UVIndex)
	case "uv_level":
		return derefStr(w.UVLevel)
	case "wind_dir":
		return derefStr(w.WindDir)
	case "wind_speed_ms":
		return derefF(w.WindSpeedMS)
	case "visibility_km":
		return derefF(w.VisibilityKm)
	}
	return nil
}

// diffWeather 逐字段比对两条天气记录，只返回真正不同的字段。
//
// 两侧的值都已经过 Quantize 抹平 float32 噪声，因此不会出现
// 「22.34 vs 22.34000015258789」这种让用户无从判断的差异。
func diffWeather(old, new stagedWeather) []FieldDiff {
	var out []FieldDiff
	for _, fl := range weatherFieldLabels {
		o, n := weatherGet(old, fl.Field), weatherGet(new, fl.Field)
		if fmt.Sprint(o) == fmt.Sprint(n) {
			continue
		}
		out = append(out, FieldDiff{Field: fl.Field, Label: fl.Label, Old: o, New: n})
	}
	return out
}

// loadExistingWeather 把库内已有天气读成与暂存记录同构的结构，
// 这样新纪录与旧纪录可以走同一个 diffWeather。
func (s *Service) loadExistingWeather(ctx context.Context) (map[string]stagedWeather, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.source_key, w.hour_ts, w.hour_local, w.local_date, w.local_month,
		       w.condition, w.temp_c, w.feels_like_c, w.dew_point_c, w.humidity,
		       w.precip_type, w.precip_mm, w.precip_prob, w.uv_index, w.uv_level,
		       w.wind_dir, w.wind_speed_ms, w.visibility_km
		FROM weather w JOIN places p ON p.id = w.place_id`)
	if err != nil {
		return nil, fmt.Errorf("读取已有天气失败: %w", err)
	}
	defer rows.Close()

	out := make(map[string]stagedWeather)
	for rows.Next() {
		var w stagedWeather
		var cond, ptype, uvlevel, wdir *string
		var temp, feel, dew, hum, pmm, pp, ws, vis *float64
		var uv *int
		if err := rows.Scan(&w.PlaceKey, &w.HourTS, &w.HourLocal, &w.LocalDate, &w.LocalMonth,
			&cond, &temp, &feel, &dew, &hum, &ptype, &pmm, &pp, &uv, &uvlevel,
			&wdir, &ws, &vis); err != nil {
			return nil, fmt.Errorf("扫描天气失败: %w", err)
		}
		w.Condition, w.PrecipType, w.UVLevel, w.WindDir = cond, ptype, uvlevel, wdir
		w.TempC, w.FeelsLikeC, w.DewPointC, w.Humidity = temp, feel, dew, hum
		w.PrecipMM, w.PrecipProb, w.WindSpeedMS, w.VisibilityKm = pmm, pp, ws, vis
		w.UVIndex = uv
		w.DedupKey = weatherDedupKey(w.PlaceKey, w.HourTS)
		out[w.DedupKey] = w
	}
	return out, rows.Err()
}

// 去重键提取。写成具体函数而不是泛型，是因为三个结构体的字段名各不相同，
// 硬凑一个接口反而更难读。
func visitKeys(rows []stagedVisit) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.DedupKey)
	}
	return out
}

func tripKeys(rows []stagedTrip) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.DedupKey)
	}
	return out
}

func weatherKeys(rows []stagedWeather) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.DedupKey)
	}
	return out
}

func derefStr(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
func derefF(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
func derefI(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
