package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/youlv/internal/importer"
	"github.com/Felix2yu/youlv/internal/store"
	"github.com/Felix2yu/youlv/internal/testdb"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	tz := time.FixedZone("Asia/Shanghai", 8*3600)
	conn := testdb.New(t)
	return New(store.New(conn, tz), importer.New(conn, t.TempDir(), tz), "test")
}

// do 发起一次请求并返回 recorder。
func do(t *testing.T, s *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是合法 JSON（%d）: %s", w.Code, w.Body.String())
	}
	return m
}

func TestHealthIsAlwaysOK(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/api/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，应为 200", w.Code)
	}
	m := decode(t, w)
	if m["ok"] != true {
		t.Errorf("ok = %v，应为 true", m["ok"])
	}
}

func TestPlacesStatsOnEmptyDB(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/api/places/stats", "")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，应为 200", w.Code)
	}
	m := decode(t, w)
	if m["total"].(float64) != 0 {
		t.Errorf("空库的 total = %v，应为 0", m["total"])
	}
	// by_origin 必须是非 nil 的对象，前端直接遍历它。
	if _, ok := m["by_origin"].(map[string]any); !ok {
		t.Errorf("by_origin 类型 = %T，应为对象", m["by_origin"])
	}
}

func TestTagsOnEmptyDB(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/api/tags", "")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，应为 200", w.Code)
	}
	m := decode(t, w)
	items, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("items 类型 = %T，应为数组", m["items"])
	}
	if len(items) != 0 {
		t.Errorf("空库标签数 = %d，应为 0", len(items))
	}
}

func TestMapTilesReturnsConfig(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/api/map/tiles", "")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，应为 200", w.Code)
	}
	m := decode(t, w)
	if m["url_template"] == "" {
		t.Error("url_template 不应为空（默认给一个可用瓦片源）")
	}
	if m["frame"] != "wgs84" {
		t.Errorf("默认 frame = %v，应为 wgs84", m["frame"])
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestServer(t)

	w := do(t, s, "GET", "/api/settings", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET 状态码 = %d，应为 200", w.Code)
	}
	if decode(t, w)["map"].(map[string]any)["frame"] != "wgs84" {
		t.Error("默认坐标系应为 wgs84")
	}

	// 部分更新：只传 map.frame。
	w = do(t, s, "PUT", "/api/settings", `{"map":{"frame":"gcj02"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT 状态码 = %d，应为 200：%s", w.Code, w.Body.String())
	}
	m := decode(t, w)
	if m["map"].(map[string]any)["frame"] != "gcj02" {
		t.Errorf("更新后 frame = %v，应为 gcj02", m["map"].(map[string]any)["frame"])
	}
	// 没传的字段应保持原值（部分更新，不是整体替换）。
	if m["map"].(map[string]any)["tiles_max_zoom"].(float64) != 19 {
		t.Errorf("未传的 tiles_max_zoom 被改成了 %v，应保持 19",
			m["map"].(map[string]any)["tiles_max_zoom"])
	}

	// 再读一次，确认落库了。
	w = do(t, s, "GET", "/api/settings", "")
	if decode(t, w)["map"].(map[string]any)["frame"] != "gcj02" {
		t.Error("更新没有落库")
	}
}

func TestSettingsRejectsUnknownKey(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "PUT", "/api/settings", `{"map":{"tiles_urls":"x"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未知键的状态码 = %d，应为 400：%s", w.Code, w.Body.String())
	}
}

func TestSettingsRejectsBadFrame(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "PUT", "/api/settings", `{"map":{"frame":"bd09"}}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法坐标系的状态码 = %d，应为 400：%s", w.Code, w.Body.String())
	}
}

func TestVisitDetailNotFound(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/api/visits/424242", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，应为 404", w.Code)
	}
}

func TestVisitDetailRejectsBadID(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/api/visits/abc", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，应为 400", w.Code)
	}
}

func TestDeleteImportBatchNotFound(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "DELETE", "/api/imports/nope", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，应为 400（批次不存在）", w.Code)
	}
}

// 未匹配的 /api/ 路径按设计回退到前端（SPA 深链由前端路由接管），
// 但绝不能变成服务端错误，也不能伪装成一次成功的 JSON 响应。
func TestUnknownAPIPathFallsBackToFrontend(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "GET", "/api/nope", "")
	if w.Code >= 500 {
		t.Fatalf("状态码 = %d，不应是服务端错误", w.Code)
	}
	if strings.Contains(w.Body.String(), `"error"`) {
		t.Errorf("未匹配的 /api/ 路径不该返回错误体：%s", w.Body.String())
	}
}
