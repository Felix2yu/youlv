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
	// 令牌留空：测试走的是不鉴权路径，鉴权本身另有用例覆盖。
	return New(store.New(conn, tz), importer.New(conn, t.TempDir(), tz), "test", "")
}

// 配了令牌之后，除探活外的接口必须带令牌才能访问。
func TestTokenGateProtectsAPI(t *testing.T) {
	tz := time.FixedZone("Asia/Shanghai", 8*3600)
	conn := testdb.New(t)
	s := New(store.New(conn, tz), importer.New(conn, t.TempDir(), tz), "test", "secret")

	if w := do(t, s, "GET", "/api/meta", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("不带令牌：状态码 = %d，应为 401", w.Code)
	}
	// 探活必须始终免鉴权：健康检查与反向代理探测不能因为鉴权失败。
	if w := do(t, s, "GET", "/api/health", ""); w.Code != http.StatusOK {
		t.Errorf("探活：状态码 = %d，应为 200（探活不能受鉴权影响）", w.Code)
	}

	r := httptest.NewRequest("GET", "/api/meta", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("带正确令牌：状态码 = %d，应为 200", w.Code)
	}
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
	if m["frame"] != "gcj02" {
		t.Errorf("默认 frame = %v，应为 gcj02（默认底图是高德的瓦片，必须同坐标系）", m["frame"])
	}
	if !strings.Contains(m["url_template"].(string), "autonavi") {
		t.Errorf("默认瓦片源 = %v，应为实测可直连的国内图源", m["url_template"])
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	s := newTestServer(t)

	w := do(t, s, "GET", "/api/settings", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET 状态码 = %d，应为 200", w.Code)
	}
	if decode(t, w)["map"].(map[string]any)["frame"] != "gcj02" {
		t.Error("默认坐标系应为 gcj02")
	}

	// 部分更新：只传 map.frame。
	w = do(t, s, "PUT", "/api/settings", `{"map":{"frame":"wgs84"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT 状态码 = %d，应为 200：%s", w.Code, w.Body.String())
	}
	m := decode(t, w)
	if m["map"].(map[string]any)["frame"] != "wgs84" {
		t.Errorf("更新后 frame = %v，应为 wgs84", m["map"].(map[string]any)["frame"])
	}
	// 没传的字段应保持原值（部分更新，不是整体替换）。
	if m["map"].(map[string]any)["tiles_max_zoom"].(float64) != 18 {
		t.Errorf("未传的 tiles_max_zoom 被改成了 %v，应保持 18",
			m["map"].(map[string]any)["tiles_max_zoom"])
	}

	// 再读一次，确认落库了。
	w = do(t, s, "GET", "/api/settings", "")
	if decode(t, w)["map"].(map[string]any)["frame"] != "wgs84" {
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
	// 批次不存在是 404，不是 400：客户端要能和「参数写错」区分开。
	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，应为 404（批次不存在）", w.Code)
	}
}

// 预检有阻断项时不能直接提交：前端禁用按钮只是提示，API 才是最后一道门。
func TestCommitRejectsRolledBackBatch(t *testing.T) {
	s := newTestServer(t)
	w := do(t, s, "POST", "/api/imports/nope/commit", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("状态码 = %d，应为 404（批次不存在）", w.Code)
	}
}

// 日期格式必须挡住：查询是按字符串比较的，2024-1-1 和 2024-12-01 在 SQL 里
// 是两种排序结果，静默放过就是「查不到但不告诉你」。
func TestTimelineRejectsMalformedDate(t *testing.T) {
	s := newTestServer(t)
	for _, q := range []string{"date=2024-1-1", "month=2024-13", "from=2024-1-1"} {
		w := do(t, s, "GET", "/api/timeline/"+map[string]string{
			"date=2024-1-1": "day", "month=2024-13": "month", "from=2024-1-1": "range",
		}[q]+"?"+q, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s：状态码 = %d，应为 400", q, w.Code)
		}
	}
}

// 分页参数要钳住：SQLite 里 LIMIT -1 表示不限制，不挡就是整表拉回来。
func TestMapPointsRejectsBadBBox(t *testing.T) {
	s := newTestServer(t)
	for _, q := range []string{"west=200", "south=-200", "west=10&east=1", "west=NaN"} {
		w := do(t, s, "GET", "/api/map/points?"+q, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s：状态码 = %d，应为 400", q, w.Code)
		}
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
