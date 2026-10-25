package api

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 手动录入到删除的完整来回：这些端点此前完全不存在，
// 页面上「记一笔」与抽屉里的编辑/删除都压在这条链上。
func TestManualVisitLifecycle(t *testing.T) {
	s := newTestServer(t)

	// 2026-10-26 10:00 +08:00
	arrived := time.Date(2026, 10, 26, 2, 0, 0, 0, time.UTC).Unix()
	departed := arrived + 3600
	body, _ := json.Marshal(map[string]any{
		"place_name":  "手动测试咖啡馆",
		"arrived_at":  arrived,
		"departed_at": departed,
		"activity":    "咖啡",
		"note":        "接口测试",
		"tags":        []string{"测试"},
		"lat":         31.2304,
		"lon":         121.4737,
	})
	w := do(t, s, "POST", "/api/visits", string(body))
	if w.Code != 200 {
		t.Fatalf("创建到访：状态码 = %d，应为 200，响应 %s", w.Code, w.Body.String())
	}
	var created struct {
		VisitID      int64 `json:"visit_id"`
		PlaceID      int64 `json:"place_id"`
		CreatedPlace bool  `json:"created_place"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("创建响应不是合法 JSON: %v", err)
	}
	if created.VisitID == 0 || created.PlaceID == 0 {
		t.Fatalf("返回了空 id：%s", w.Body.String())
	}
	if !created.CreatedPlace {
		t.Error("地点是新建的，created_place 应为 true")
	}

	// 当天时间轴要能查出来。
	if w := do(t, s, "GET", "/api/timeline/day?date=2026-10-26", ""); w.Code != 200 {
		t.Errorf("当天时间轴：状态码 = %d，应为 200", w.Code)
	} else if m := decode(t, w); len(m["items"].([]any)) != 1 {
		t.Errorf("当天应有 1 条到访，实际 %d", len(m["items"].([]any)))
	}

	// 地点详情：新地点应带 1 次到访。
	w = do(t, s, "GET", "/api/places/"+itoa(created.PlaceID), "")
	if w.Code != 200 {
		t.Fatalf("地点详情：状态码 = %d，应为 200", w.Code)
	}
	if m := decode(t, w); m["visit_count"].(float64) != 1 {
		t.Errorf("地点到访次数 = %v，应为 1", m["visit_count"])
	}

	// 清掉离开时间：约定用 0 表示「显式清空」。
	patch := `{"departed_at":0}`
	if w := do(t, s, "PATCH", "/api/visits/"+itoa(created.VisitID), patch); w.Code != 200 {
		t.Fatalf("清空离开时间：状态码 = %d，应为 200，响应 %s", w.Code, w.Body.String())
	}
	w = do(t, s, "GET", "/api/visits/"+itoa(created.VisitID), "")
	if w.Code != 200 {
		t.Fatalf("到访详情：状态码 = %d，应为 200", w.Code)
	}
	if m := decode(t, w); m["departed_at"] != nil {
		t.Errorf("离开时间应已清空，实际 %v", m["departed_at"])
	}

	// 离开早于抵达必须被挡住。
	bad := `{"departed_at":1}`
	if w := do(t, s, "PATCH", "/api/visits/"+itoa(created.VisitID), bad); w.Code != 400 {
		t.Errorf("离开早于抵达：状态码 = %d，应为 400", w.Code)
	}

	if w := do(t, s, "DELETE", "/api/visits/"+itoa(created.VisitID), ""); w.Code != 204 {
		t.Fatalf("删除到访：状态码 = %d，应为 204", w.Code)
	}
	if w := do(t, s, "GET", "/api/visits/"+itoa(created.VisitID), ""); w.Code != 404 {
		t.Errorf("删除后再取：状态码 = %d，应为 404", w.Code)
	}
}

// 参数校验要拦在 SQL 之前，别让「2026-1-1」这种写法静默查成空。
func TestTripsAndExportRejectBadDates(t *testing.T) {
	s := newTestServer(t)

	if w := do(t, s, "GET", "/api/trips?from=2026-1-1", ""); w.Code != 400 {
		t.Errorf("trips 非法 from：状态码 = %d，应为 400", w.Code)
	}
	if w := do(t, s, "GET", "/api/trips?from=2026-10-01&to=2026-10-31", ""); w.Code != 200 {
		t.Errorf("trips 合法范围：状态码 = %d，应为 200", w.Code)
	}
	if w := do(t, s, "GET", "/api/export/visits.csv?to=2026-13-01", ""); w.Code != 400 {
		t.Errorf("导出非法 to：状态码 = %d，应为 400", w.Code)
	}
}

// 导出必须带 BOM 与表头：Excel 靠 BOM 认 UTF-8，没有它中文全是乱码。
func TestExportVisitsCSV(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/api/export/visits.csv", "")
	if w.Code != 200 {
		t.Fatalf("导出：状态码 = %d，应为 200", w.Code)
	}
	body := w.Body.Bytes()
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Fatalf("导出内容缺少 UTF-8 BOM")
	}
	text := string(body[3:])
	if !strings.Contains(text, "WGS纬度") {
		t.Errorf("表头缺列：%s", strings.SplitN(text, "\n", 2)[0])
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q，应为 text/csv", ct)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
