package store

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// 设置项的 KV 键名与 JSON 路径一一对应（map.tiles_url ↔ {"map":{"tiles_url":…}}），
// 这样用户在 SQLite 里手工改一行也能生效，不必猜键名。
const (
	keyMapTilesURL         = "map.tiles_url"
	keyMapTilesAttribution = "map.tiles_attribution"
	keyMapTilesMaxZoom     = "map.tiles_max_zoom"
	keyMapTilesSubdomains  = "map.tiles_subdomains"
	keyMapFrame            = "map.frame"
	keyTimelineFrame       = "timeline.frame"
	keyWeatherApprox       = "weather.approx"
)

// 默认瓦片源：CARTO 的浅色底图。
//
// 注意这里的 {s} 子域占位符要求 subdomains 一起生效；Leaflet 侧会把它
// 展开成 a/b/c/d 四个子域。
const defaultTilesURL = "https://{s}.basemaps.cartocdn.com/light_all/{z}/{x}/{y}{r}.png"

// Settings 是应用设置。
type Settings struct {
	Map struct {
		// TilesURL 为空表示不提供底图，前端应只留自绘层。
		TilesURL         string `json:"tiles_url"`
		TilesAttribution string `json:"tiles_attribution"`
		TilesMaxZoom     int    `json:"tiles_max_zoom"`
		TilesSubdomains  string `json:"tiles_subdomains"`
		// Frame 是地图渲染用的坐标系：wgs84 或 gcj02。
		// 底图是 GCJ-02 的（高德/腾讯/国内图源）时必须设成 gcj02，
		// 否则点位会整体偏移三四百米。
		Frame string `json:"frame"`
	} `json:"map"`
	Timeline struct {
		Frame string `json:"frame"`
	} `json:"timeline"`
	Weather struct {
		// Approx 为真时允许用邻近小时/邻近地点的天气近似。
		Approx bool `json:"approx"`
	} `json:"weather"`
}

// SettingsPatch 是设置的部分更新：指针字段区分「没传」与「传了零值」。
type SettingsPatch struct {
	Map *struct {
		TilesURL         *string `json:"tiles_url"`
		TilesAttribution *string `json:"tiles_attribution"`
		TilesMaxZoom     *int    `json:"tiles_max_zoom"`
		TilesSubdomains  *string `json:"tiles_subdomains"`
		Frame            *string `json:"frame"`
	} `json:"map"`
	Timeline *struct {
		Frame *string `json:"frame"`
	} `json:"timeline"`
	Weather *struct {
		Approx *bool `json:"approx"`
	} `json:"weather"`
}

// 允许的坐标系取值。
var validFrames = map[string]bool{"wgs84": true, "gcj02": true}

// Settings 读取当前设置，缺省项用默认值补齐。
func (s *Store) Settings(ctx context.Context) (Settings, error) {
	kv, err := s.settingsKV(ctx)
	if err != nil {
		return Settings{}, err
	}
	var out Settings
	out.Map.TilesURL = kvStr(kv, keyMapTilesURL, defaultTilesURL)
	out.Map.TilesAttribution = kvStr(kv, keyMapTilesAttribution, "© OpenStreetMap · © CARTO")
	out.Map.TilesMaxZoom = kvInt(kv, keyMapTilesMaxZoom, 19)
	out.Map.TilesSubdomains = kvStr(kv, keyMapTilesSubdomains, "a,b,c,d")
	out.Map.Frame = kvStr(kv, keyMapFrame, "wgs84")
	out.Timeline.Frame = kvStr(kv, keyTimelineFrame, "wgs84")
	out.Weather.Approx = kvBool(kv, keyWeatherApprox, true)
	return out, nil
}

// SaveSettings 写入设置并返回合并后的完整结果。
//
// 未知键由调用方（API 层）的 DisallowUnknownFields 拦下并报 400——
// 打错一个键名却静默不生效，是最难查的一类问题。
func (s *Store) SaveSettings(ctx context.Context, p SettingsPatch) (Settings, error) {
	kv := map[string]string{}

	if p.Map != nil {
		if p.Map.TilesURL != nil {
			kv[keyMapTilesURL] = *p.Map.TilesURL
		}
		if p.Map.TilesAttribution != nil {
			kv[keyMapTilesAttribution] = *p.Map.TilesAttribution
		}
		if p.Map.TilesMaxZoom != nil {
			z := *p.Map.TilesMaxZoom
			if z < 1 || z > 22 {
				return Settings{}, fmt.Errorf("瓦片最大层级应在 1~22 之间，收到 %d", z)
			}
			kv[keyMapTilesMaxZoom] = strconv.Itoa(z)
		}
		if p.Map.TilesSubdomains != nil {
			kv[keyMapTilesSubdomains] = *p.Map.TilesSubdomains
		}
		if p.Map.Frame != nil {
			if !validFrames[*p.Map.Frame] {
				return Settings{}, fmt.Errorf("未知坐标系 %q，应为 wgs84 或 gcj02", *p.Map.Frame)
			}
			kv[keyMapFrame] = *p.Map.Frame
		}
	}
	if p.Timeline != nil && p.Timeline.Frame != nil {
		if !validFrames[*p.Timeline.Frame] {
			return Settings{}, fmt.Errorf("未知坐标系 %q，应为 wgs84 或 gcj02", *p.Timeline.Frame)
		}
		kv[keyTimelineFrame] = *p.Timeline.Frame
	}
	if p.Weather != nil && p.Weather.Approx != nil {
		if *p.Weather.Approx {
			kv[keyWeatherApprox] = "1"
		} else {
			kv[keyWeatherApprox] = "0"
		}
	}

	if len(kv) > 0 {
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			return Settings{}, err
		}
		defer tx.Rollback()
		for k, v := range kv {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO settings(key, value, updated_at) VALUES(?,?,unixepoch())
				 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=unixepoch()`,
				k, v); err != nil {
				return Settings{}, err
			}
		}
		if err := tx.Commit(); err != nil {
			return Settings{}, err
		}
	}
	return s.Settings(ctx)
}

func (s *Store) settingsKV(ctx context.Context) (map[string]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func kvStr(kv map[string]string, key, def string) string {
	if v, ok := kv[key]; ok {
		return v
	}
	return def
}

func kvInt(kv map[string]string, key string, def int) int {
	v, ok := kv[key]
	if !ok {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return def
	}
	return n
}

func kvBool(kv map[string]string, key string, def bool) bool {
	v, ok := kv[key]
	if !ok {
		return def
	}
	switch strings.TrimSpace(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
