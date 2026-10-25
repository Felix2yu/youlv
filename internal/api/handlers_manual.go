package api

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Felix2yu/youlv/internal/importer"
	"github.com/Felix2yu/youlv/internal/store"
)

// 本文件是后补的读端点（地点详情、交通段、导出）与手动写端点。

// ---------- 地点详情 ----------

func (s *Server) placeDetail(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "地点 id 不合法")
		return nil
	}
	d, err := s.st.PlaceDetail(r.Context(), id, queryFrame(r))
	if err != nil {
		if errors.Is(err, sqlNoRows) {
			writeErr(w, http.StatusNotFound, "地点不存在")
			return nil
		}
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}

func (s *Server) placeVisits(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "地点 id 不合法")
		return nil
	}
	list, err := s.st.PlaceVisits(r.Context(), id,
		queryLimit(r, 50, maxPageSize), queryOffset(r), queryFrame(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

// ---------- 交通段 ----------

func (s *Server) trips(w http.ResponseWriter, r *http.Request) error {
	from, ok := queryDate(r, "from")
	if r.URL.Query().Get("from") != "" && !ok {
		writeErr(w, http.StatusBadRequest, "from 参数需要是 YYYY-MM-DD")
		return nil
	}
	to, ok := queryDate(r, "to")
	if r.URL.Query().Get("to") != "" && !ok {
		writeErr(w, http.StatusBadRequest, "to 参数需要是 YYYY-MM-DD")
		return nil
	}
	list, total, err := s.st.Trips(r.Context(), store.TripFilter{
		From: from, To: to, Mode: query(r, "mode", ""),
		Limit: queryLimit(r, 100, maxPageSize), Offset: queryOffset(r),
	})
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total})
	return nil
}

// ---------- 导出 ----------

// exportVisitsCSV 导出到访明细。两个坐标系都给，避免用户自己换算。
func (s *Server) exportVisitsCSV(w http.ResponseWriter, r *http.Request) error {
	from, ok := queryDate(r, "from")
	if r.URL.Query().Get("from") != "" && !ok {
		writeErr(w, http.StatusBadRequest, "from 参数需要是 YYYY-MM-DD")
		return nil
	}
	to, ok := queryDate(r, "to")
	if r.URL.Query().Get("to") != "" && !ok {
		writeErr(w, http.StatusBadRequest, "to 参数需要是 YYYY-MM-DD")
		return nil
	}
	rows, err := s.st.ExportVisits(r.Context(), from, to, queryLimit(r, 50000, 100000))
	if err != nil {
		return err
	}
	name := "youlv-到访"
	if from != "" || to != "" {
		name += "-" + from + "-" + to
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.csv"; filename*=UTF-8''%s.csv`, name, urlEscape(name)))
	// Excel 靠 BOM 认 UTF-8，没有它中文全是乱码。
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := newCSVWriter(w)
	_ = cw.Write([]string{"日期", "抵达时刻", "离开时刻", "停留(分钟)", "地点", "城市",
		"活动", "天气", "气温", "标签", "备注", "WGS纬度", "WGS经度", "GCJ纬度", "GCJ经度"})
	for _, v := range rows {
		_ = cw.Write([]string{
			v.LocalDate, unixToLocal(v.ArrivedAt), unixToLocalPtr(v.DepartedAt),
			durMinutes(v.DurationS), v.Place, v.City, v.Activity,
			v.Condition, tempStr(v.TempC), v.Tags, v.Note,
			coordStr(v.Lat), coordStr(v.Lon), coordStr(v.LatGCJ), coordStr(v.LonGCJ),
		})
	}
	cw.Flush()
	return nil
}

// ---------- 手动写入 ----------

func (s *Server) visitCreate(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		PlaceName  string   `json:"place_name"`
		PlaceID    int64    `json:"place_id"`
		ArrivedAt  int64    `json:"arrived_at"`
		DepartedAt *int64   `json:"departed_at"`
		Activity   string   `json:"activity"`
		Note       string   `json:"note"`
		Tags       []string `json:"tags"`
		Lat        *float64 `json:"lat"`
		Lon        *float64 `json:"lon"`
		City       string   `json:"city"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败")
		return nil
	}
	res, err := s.imp.CreateVisit(r.Context(), importer.ManualVisit{
		PlaceName: body.PlaceName, PlaceID: body.PlaceID,
		ArrivedAt: body.ArrivedAt, DepartedAt: body.DepartedAt,
		Activity: body.Activity, Note: body.Note, Tags: body.Tags,
		Lat: body.Lat, Lon: body.Lon, City: body.City,
	})
	if err != nil {
		writeImportErr(w, err)
		return nil
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) visitUpdate(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "到访 id 不合法")
		return nil
	}
	var body struct {
		Note       *string   `json:"note"`
		Activity   *string   `json:"activity"`
		Tags       *[]string `json:"tags"`
		DepartedAt *int64    `json:"departed_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "请求体解析失败")
		return nil
	}
	if err := s.imp.UpdateVisit(r.Context(), id, importer.VisitPatch{
		Note: body.Note, Activity: body.Activity, Tags: body.Tags, DepartedAt: body.DepartedAt,
	}); err != nil {
		writeImportErr(w, err)
		return nil
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	return nil
}

func (s *Server) visitDelete(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "到访 id 不合法")
		return nil
	}
	if err := s.imp.DeleteVisit(r.Context(), id); err != nil {
		if errors.Is(err, importer.ErrBatchNotFound) {
			writeErr(w, http.StatusNotFound, "到访不存在")
			return nil
		}
		writeImportErr(w, err)
		return nil
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func urlEscape(s string) string {
	return strings.ReplaceAll(s, " ", "%20")
}

// sqlNoRows 让调用方能统一判断「查不到」。
var sqlNoRows = sql.ErrNoRows

func newCSVWriter(w io.Writer) *csv.Writer { return csv.NewWriter(w) }

// 展示用的一律按东八区：库里存的本地时间就是这个时区算出来的。
var displayTZ = time.FixedZone("Asia/Shanghai", 8*3600)

func unixToLocal(ts int64) string {
	return time.Unix(ts, 0).In(displayTZ).Format("2006-01-02 15:04")
}

func unixToLocalPtr(ts *int64) string {
	if ts == nil {
		return ""
	}
	return unixToLocal(*ts)
}

func durMinutes(d *int64) string {
	if d == nil {
		return ""
	}
	return strconv.FormatInt(*d/60, 10)
}

func tempStr(c *float64) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("%.1f", *c)
}

func coordStr(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.6f", *v)
}
