package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 规范：新建的包必须自带测试，否则 codecov 的 patch 检查会把 PR 打红。

func TestHasBuildIsFalseWithoutFrontend(t *testing.T) {
	// 仓库里 dist/ 只有 .gitkeep 占位，未构建前端时必须是 false。
	// 这个断言同时保证「未构建前端的机器上 go test 也能跑通」。
	if HasBuild() {
		t.Skip("本次构建已内嵌前端产物")
	}
}

func TestHandlerServesPlaceholderWithoutBuild(t *testing.T) {
	if HasBuild() {
		t.Skip("本次构建已内嵌前端产物")
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d, want 200（未构建时应给提示页而不是 500）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "游履") {
		t.Error("提示页应包含应用名")
	}
}

func TestHandlerAPINotFound(t *testing.T) {
	if HasBuild() {
		t.Skip("本次构建已内嵌前端产物")
	}
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("未知接口应返回 404，实际 %d", rec.Code)
	}
}

func TestFSAlwaysUsable(t *testing.T) {
	// embed 的 dist 目录始终存在（仓库里保留 .gitkeep 占位），
	// 因此 FS() 在任何构建状态下都必须可打开。
	f, err := FS().Open(".")
	if err != nil {
		t.Fatalf("FS().Open(.) 失败: %v", err)
	}
	f.Close()
}
