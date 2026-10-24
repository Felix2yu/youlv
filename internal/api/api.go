// Package api 是 HTTP 层：路由注册、统一响应与错误映射。
//
// 路由用标准库 http.ServeMux（Go 1.22+ 的方法路由语法），不引入 chi/gin。
package api

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/yufei/youlv/internal/importer"
	"github.com/yufei/youlv/internal/store"
	"github.com/yufei/youlv/internal/web"
)

// Server 持有依赖并对外暴露 http.Handler。
type Server struct {
	st      *store.Store
	imp     *importer.Service
	mux     *http.ServeMux
	root    http.Handler
	version string
}

// New 构造路由表。
func New(st *store.Store, imp *importer.Service, version string) *Server {
	s := &Server{st: st, imp: imp, mux: http.NewServeMux(), version: version}
	s.routes()
	// 未匹配 /api/ 的路径一律回前端（SPA 深链由前端路由接管）。
	s.mux.Handle("/", web.Handler())
	s.root = s.recoverer(s.mux)
	return s
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
	s.mux.HandleFunc("GET /api/visits/{id}", h(s.visitDetail))

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

func queryFloat(r *http.Request, key string, def float64) float64 {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}
