// Package api 是 HTTP 层：路由注册、统一响应与错误映射。
//
// 路由用标准库 http.ServeMux（Go 1.22+ 的方法路由语法），不引入 chi/gin。
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/youlv/internal/importer"
	"github.com/Felix2yu/youlv/internal/store"
	"github.com/Felix2yu/youlv/internal/web"
)

// Server 持有依赖并对外暴露 http.Handler。
type Server struct {
	st      *store.Store
	imp     *importer.Service
	mux     *http.ServeMux
	root    http.Handler
	version string
}

// New 构造路由表。token 非空时开启接口鉴权（见 tokenGate）。
func New(st *store.Store, imp *importer.Service, version string, token string) *Server {
	s := &Server{st: st, imp: imp, mux: http.NewServeMux(), version: version}
	s.routes()
	// 未匹配 /api/ 的路径一律回前端（SPA 深链由前端路由接管）。
	s.mux.Handle("/", web.Handler())
	s.root = s.recoverer(tokenGate(token, s.mux))
	return s
}

// tokenGate 是可选的访问令牌校验。
//
// 没有配置 YOULV_TOKEN 时完全不设防：自托管单人应用跑在可信内网是合理选择，
// 但「任何人都能 POST /api/imports/purge 清空全部数据」这个事实必须被说出来，
// 所以 main 在启动日志里会写明当前是哪种状态。
//
// 校验顺序：Authorization: Bearer <token> → X-Auth-Token → ?token=。
// 留 query 这一路是因为 CSV 导出是浏览器直接下载，<a href> 带不上请求头。
func tokenGate(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		if subtleCompare(bearerToken(r), token) || subtleCompare(r.URL.Query().Get("token"), token) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, "缺少或无效的访问令牌")
	})
}

func bearerToken(r *http.Request) string {
	if v := r.Header.Get("X-Auth-Token"); v != "" {
		return v
	}
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(h), strings.ToLower(prefix)) {
		return h[len(prefix):]
	}
	return ""
}

// subtleCompare 用常量时间比较，避免按字节逐位泄露令牌前缀。
func subtleCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.root.ServeHTTP(w, r) }

func (s *Server) routes() {
	// h 把「返回 error 的 handler」适配成 http.HandlerFunc，并统一写错误。
	h := func(fn func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				log.Printf("[api] %s %s: %v", r.Method, r.URL.Path, err)
				writeErr(w, http.StatusInternalServerError, "服务端开小差了，请稍后重试")
			}
		}
	}

	// 探活端点必须免鉴权，且不能依赖数据库可用。
	s.mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "version": s.version, "service": "youlv",
		})
	})

	s.mux.HandleFunc("GET /api/meta", h(s.meta))

	s.mux.HandleFunc("GET /api/timeline/day", h(s.timelineDay))
	s.mux.HandleFunc("GET /api/timeline/month", h(s.timelineMonth))
	s.mux.HandleFunc("GET /api/timeline/range", h(s.timelineRange))

	s.mux.HandleFunc("GET /api/places", h(s.places))
	s.mux.HandleFunc("GET /api/places/stats", h(s.placesStats))
	s.mux.HandleFunc("GET /api/activities", h(s.activities))
	s.mux.HandleFunc("GET /api/tags", h(s.tags))
	s.mux.HandleFunc("GET /api/places/{id}", h(s.placeDetail))
	s.mux.HandleFunc("GET /api/places/{id}/visits", h(s.placeVisits))
	s.mux.HandleFunc("GET /api/trips", h(s.trips))
	s.mux.HandleFunc("GET /api/visits/{id}", h(s.visitDetail))
	// 手动录入：写侧仍在 importer，API 只做参数校验与转发。
	s.mux.HandleFunc("POST /api/visits", h(s.visitCreate))
	s.mux.HandleFunc("PATCH /api/visits/{id}", h(s.visitUpdate))
	s.mux.HandleFunc("DELETE /api/visits/{id}", h(s.visitDelete))
	// 导出：自托管应用必须让用户把自己的数据拿走。
	s.mux.HandleFunc("GET /api/export/visits.csv", h(s.exportVisitsCSV))

	s.mux.HandleFunc("GET /api/settings", h(s.settings))
	s.mux.HandleFunc("PUT /api/settings", h(s.settingsUpdate))

	s.mux.HandleFunc("GET /api/stats/overview", h(s.statsOverview))
	s.mux.HandleFunc("GET /api/stats/activities", h(s.statsActivities))
	s.mux.HandleFunc("GET /api/stats/cities", h(s.statsCities))
	s.mux.HandleFunc("GET /api/stats/commute", h(s.statsCommute))
	s.mux.HandleFunc("GET /api/stats/hourly", h(s.statsHourly))

	s.mux.HandleFunc("GET /api/map/points", h(s.mapPoints))
	s.mux.HandleFunc("GET /api/map/bounds", h(s.mapBounds))
	s.mux.HandleFunc("GET /api/map/tiles", h(s.mapTiles))

	s.mux.HandleFunc("GET /api/weather", h(s.weather))

	s.mux.HandleFunc("POST /api/imports/preview", h(s.importPreview))
	s.mux.HandleFunc("POST /api/imports/detect", h(s.importDetect))
	s.mux.HandleFunc("POST /api/imports/purge", h(s.importPurge))
	s.mux.HandleFunc("POST /api/imports/{batchNo}/commit", h(s.importCommit))
	s.mux.HandleFunc("POST /api/imports/{batchNo}/rollback", h(s.importRollback))
	s.mux.HandleFunc("GET /api/imports", h(s.importList))
	s.mux.HandleFunc("GET /api/imports/{batchNo}", h(s.importDetail))
	s.mux.HandleFunc("DELETE /api/imports/{batchNo}", h(s.importDelete))
	s.mux.HandleFunc("GET /api/imports/{batchNo}/issues", h(s.importIssues))
	s.mux.HandleFunc("GET /api/imports/{batchNo}/issues.csv", h(s.importIssuesCSV))
}

// recoverer 兜住 handler 里的 panic，避免一次意外把整个服务打挂。
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[api] panic %s %s: %v", r.Method, r.URL.Path, rec)
				writeErr(w, http.StatusInternalServerError, "服务端开小差了，请稍后重试")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---------- 响应 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ---------- 入参 ----------

func query(r *http.Request, key, def string) string {
	if v := strings.TrimSpace(r.URL.Query().Get(key)); v != "" {
		return v
	}
	return def
}

func queryInt(r *http.Request, key string, def int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// ---------- 入参校验 ----------
//
// 这几个函数回答的是「参数合不合法」，而不是「参数有没有」。
// 日期/月份/坐标系从前都是直接透传：日期只做字符串比较，于是
// from=2024-1-1 会因为字典序（"2024-1-1" > "2024-12-01"）静默漏掉
// 10~12 月的全部数据——查询结果错了却不报错，是最难发现的一类问题。

// ErrBadParam 表示参数格式不对，调用方据此返回 400。
var ErrBadParam = errors.New("参数不合法")

// queryDate 校验 YYYY-MM-DD。缺零、越界月份一律拒绝。
func queryDate(r *http.Request, key string) (string, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return "", false
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", false
	}
	return v, true
}

// queryMonth 校验 YYYY-MM。
func queryMonth(r *http.Request, key string) (string, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return "", false
	}
	if _, err := time.Parse("2006-01", v); err != nil {
		return "", false
	}
	return v, true
}

// queryFrame 只放行两种坐标系，未知值按默认处理（不报错，因为它是展示偏好）。
func queryFrame(r *http.Request) string {
	if f := strings.TrimSpace(r.URL.Query().Get("frame")); f == "gcj02" {
		return "gcj02"
	}
	return "wgs84"
}

// queryBBoxFloat 读一个经纬度边界值。NaN / Inf 也要挡：strconv 会接受
// "NaN"，它们穿过 SQL 不报错，只是让 BETWEEN 恒假，结果就是「地图空白」。
func queryBBoxFloat(r *http.Request, key string, def, min, max float64) (float64, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def, true
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < min || f > max {
		return 0, false
	}
	return f, true
}

// maxPageSize 是任意列表端点单页的上限。
const maxPageSize = 1000

// queryBool 按 1/0/true/false/yes/no/on/off 判读布尔值，与设置项的 kvBool
// 保持同一套语义。缺失时返回 def。
func queryBool(r *http.Request, key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

// queryLimit 把分页大小钳到 [1, max]。
//
// SQLite 里 LIMIT -1 表示「不限制」，不钳制的话 ?limit=-1 一次就能把整表拉回来。
func queryLimit(r *http.Request, def, max int) int {
	n := queryInt(r, "limit", def)
	if n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// queryOffset 把偏移量钳到不小于 0。
func queryOffset(r *http.Request) int {
	n := queryInt(r, "offset", 0)
	if n < 0 {
		return 0
	}
	return n
}
