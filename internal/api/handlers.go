package api

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Felix2yu/youlv/internal/csvin"
	"github.com/Felix2yu/youlv/internal/importer"
	"github.com/Felix2yu/youlv/internal/store"
)

// ---------- 元信息 ----------

func (s *Server) meta(w http.ResponseWriter, r *http.Request) error {
	m, err := s.st.Meta(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, m)
	return nil
}

// ---------- 时间轴 ----------

func (s *Server) timelineDay(w http.ResponseWriter, r *http.Request) error {
	date, ok := queryDate(r, "date")
	if !ok {
		// 格式不对必须报错：查询按字符串比较，2024-1-1 和 2024-01-01
		// 在 SQL 里是两回事，静默放过就是「查不到但不告诉你」。
		writeErr(w, http.StatusBadRequest, "date 参数需要是 YYYY-MM-DD（例如 2026-10-26）")
		return nil
	}
	list, err := s.st.TimelineDay(r.Context(), date, queryFrame(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (s *Server) timelineMonth(w http.ResponseWriter, r *http.Request) error {
	month, ok := queryMonth(r, "month")
	if !ok {
		writeErr(w, http.StatusBadRequest, "month 参数需要是 YYYY-MM（例如 2026-10）")
		return nil
	}
	days, err := s.st.TimelineMonth(r.Context(), month)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days})
	return nil
}

func (s *Server) timelineRange(w http.ResponseWriter, r *http.Request) error {
	from, fromOK := queryDate(r, "from")
	if r.URL.Query().Get("from") != "" && !fromOK {
		writeErr(w, http.StatusBadRequest, "from 参数需要是 YYYY-MM-DD")
		return nil
	}
	to, toOK := queryDate(r, "to")
	if r.URL.Query().Get("to") != "" && !toOK {
		writeErr(w, http.StatusBadRequest, "to 参数需要是 YYYY-MM-DD")
		return nil
	}
	f := store.TimelineFilter{
		From: from, To: to,
		Q: query(r, "q", ""), Activity: query(r, "activity", ""),
		PlaceID: int64(queryInt(r, "place_id", 0)),
		Limit:   queryLimit(r, 100, maxPageSize), Offset: queryOffset(r),
	}
	list, total, err := s.st.TimelineRange(r.Context(), f, queryFrame(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total})
	return nil
}

// ---------- 地点与活动 ----------

func (s *Server) places(w http.ResponseWriter, r *http.Request) error {
	f := store.PlaceFilter{
		Q: query(r, "q", ""), City: query(r, "city", ""),
		Origin: query(r, "origin", ""), Sort: query(r, "sort", ""),
		HasCoord: r.URL.Query().Get("has_coord") == "1",
		Limit:    queryLimit(r, 100, maxPageSize), Offset: queryOffset(r),
	}
	list, total, err := s.st.Places(r.Context(), f)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total})
	return nil
}

func (s *Server) activities(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.st.DB.QueryContext(r.Context(),
		`SELECT name, color, icon, sort_order FROM activities ORDER BY sort_order, name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type act struct {
		Name  string `json:"name"`
		Color string `json:"color"`
		Icon  string `json:"icon"`
		Sort  int    `json:"sort_order"`
	}
	out := []act{}
	for rows.Next() {
		var a act
		if err := rows.Scan(&a.Name, &a.Color, &a.Icon, &a.Sort); err != nil {
			return err
		}
		out = append(out, a)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

// ---------- 统计 ----------

func (s *Server) statsOverview(w http.ResponseWriter, r *http.Request) error {
	m, err := s.st.Meta(r.Context())
	if err != nil {
		return err
	}
	var totalDur, days sql.NullInt64
	if err := s.st.DB.QueryRowContext(r.Context(),
		`SELECT COALESCE(SUM(duration_s),0), COUNT(DISTINCT local_date) FROM visits`).
		Scan(&totalDur, &days); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"visits": m.Visits, "places": m.Places, "trips": m.Trips,
		"cities": m.Cities, "days": days.Int64,
		"total_duration_s": totalDur.Int64,
		"first_date":       m.FirstDate, "last_date": m.LastDate,
	})
	return nil
}

func (s *Server) statsActivities(w http.ResponseWriter, r *http.Request) error {
	list, err := s.st.StatsActivities(r.Context(), query(r, "from", ""), query(r, "to", ""))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (s *Server) statsCities(w http.ResponseWriter, r *http.Request) error {
	list, err := s.st.StatsCities(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (s *Server) statsCommute(w http.ResponseWriter, r *http.Request) error {
	list, err := s.st.StatsCommute(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (s *Server) statsHourly(w http.ResponseWriter, r *http.Request) error {
	list, err := s.st.StatsHourly(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

// ---------- 地图 ----------

func (s *Server) mapPoints(w http.ResponseWriter, r *http.Request) error {
	// 边界值逐个校验：NaN 会被 ParseFloat 接受、越界值（如 south=-200）不会报错，
	// 两者都只是让 BETWEEN 恒假，表现为「地图一片空白」且看不出原因。
	west, ok := queryBBoxFloat(r, "west", -180, -180, 180)
	if !ok {
		writeErr(w, http.StatusBadRequest, "west 不是合法经度")
		return nil
	}
	south, ok := queryBBoxFloat(r, "south", -90, -90, 90)
	if !ok {
		writeErr(w, http.StatusBadRequest, "south 不是合法纬度")
		return nil
	}
	east, ok := queryBBoxFloat(r, "east", 180, -180, 180)
	if !ok {
		writeErr(w, http.StatusBadRequest, "east 不是合法经度")
		return nil
	}
	north, ok := queryBBoxFloat(r, "north", 90, -90, 90)
	if !ok {
		writeErr(w, http.StatusBadRequest, "north 不是合法纬度")
		return nil
	}
	if west > east {
		writeErr(w, http.StatusBadRequest, "west 不能大于 east")
		return nil
	}
	if south > north {
		writeErr(w, http.StatusBadRequest, "south 不能大于 north")
		return nil
	}
	bbox := store.BBox{West: west, South: south, East: east, North: north}
	list, err := s.st.MapPoints(r.Context(), bbox)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (s *Server) mapBounds(w http.ResponseWriter, r *http.Request) error {
	b, err := s.st.Bounds(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, b)
	return nil
}

// ---------- 天气 ----------

func (s *Server) weather(w http.ResponseWriter, r *http.Request) error {
	placeID := int64(queryInt(r, "place_id", 0))
	if placeID == 0 {
		writeErr(w, http.StatusBadRequest, "缺少 place_id")
		return nil
	}
	at := int64(queryInt(r, "at", 0))
	if at == 0 {
		at = nowUnix()
	}
	// 用和设置项一致的布尔判读：从前写的是 != "0"，于是 ?approx=false
	// 也被当成 true，用户关不掉近似天气。
	w0, err := s.st.WeatherFor(r.Context(), placeID, at, queryBool(r, "approx", true))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, w0)
	return nil
}

// ---------- 导入 ----------

// maxUploadBytes 是**整个请求体**的硬上限。
//
// ParseMultipartForm 的参数只是「超出后溢写到临时文件」的水位，并不限制总量——
// 一个大文件照样会在随后被整体读进内存。所以先套一层 MaxBytesReader：
// 四份 Rond CSV 合计约 2 MB，64 MB 对个人数据足够宽松，同时把内存占用封住。
const maxUploadBytes = 64 << 20

// spillToDiskBytes 是 multipart 解析的内存水位：超出的部分写临时文件。
const spillToDiskBytes = 8 << 20

// errUploadTooLarge 表示请求体超过 maxUploadBytes。
var errUploadTooLarge = errors.New("上传内容超出上限")

// readUploads 解析 multipart 表单并读出全部文件。
func readUploads(w http.ResponseWriter, r *http.Request) ([]importer.UploadedFile, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(spillToDiskBytes); err != nil {
		var mx *http.MaxBytesError
		if errors.As(err, &mx) {
			return nil, errUploadTooLarge
		}
		return nil, errors.New("上传解析失败")
	}
	form := r.MultipartForm
	if form == nil || len(form.File) == 0 {
		return nil, errors.New("没有收到文件")
	}
	var files []importer.UploadedFile
	for _, fh := range form.File {
		for _, h := range fh {
			f, err := h.Open()
			if err != nil {
				return nil, fmt.Errorf("读取文件失败：%s", h.Filename)
			}
			buf := make([]byte, h.Size)
			if _, err := io.ReadFull(f, buf); err != nil {
				f.Close()
				return nil, fmt.Errorf("读取文件失败：%s", h.Filename)
			}
			f.Close()
			files = append(files, importer.UploadedFile{
				Name: h.Filename, Size: h.Size, Content: buf,
			})
		}
	}
	return files, nil
}

// writeImportErr 统一处理导入类错误。
//
// 只有 importer.UserError（文案本身就是写给用户看的）才原样返回；其余错误
// 可能夹着文件路径、SQL 片段、内部状态，一律只进日志、对外给通用文案。
func writeImportErr(w http.ResponseWriter, err error) {
	var ue *importer.UserError
	switch {
	case errors.Is(err, importer.ErrBatchNotFound):
		writeErr(w, http.StatusNotFound, "批次不存在")
	case errors.Is(err, importer.ErrBatchHasData):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.As(err, &ue):
		writeErr(w, http.StatusBadRequest, ue.Msg)
	default:
		log.Printf("[api] 导入失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "服务端开小差了，请稍后重试")
	}
}

func (s *Server) importPreview(w http.ResponseWriter, r *http.Request) error {
	files, err := readUploads(w, r)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("上传内容超过 %d MB，请分几批导入", maxUploadBytes>>20))
			return nil
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil
	}

	opt, err := optionsFromRequest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil
	}
	rep, err := s.imp.Preview(r.Context(), files, opt)
	if err != nil {
		writeImportErr(w, err)
		return nil
	}
	writeJSON(w, http.StatusOK, rep)
	return nil
}

// importDetect 只做类型探测，供前端拖入文件后立即回显识别结果。
func (s *Server) importDetect(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(spillToDiskBytes); err != nil {
		var mx *http.MaxBytesError
		if errors.As(err, &mx) {
			writeErr(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("上传内容超过 %d MB，请分几批导入", maxUploadBytes>>20))
			return nil
		}
		writeErr(w, http.StatusBadRequest, "上传解析失败")
		return nil
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File) == 0 {
		writeErr(w, http.StatusBadRequest, "没有收到文件")
		return nil
	}
	type result struct {
		File       string   `json:"file"`
		Kind       string   `json:"kind"`
		Label      string   `json:"label"`
		Confidence float64  `json:"confidence"`
		Rows       int      `json:"rows"`
		Missing    []string `json:"missing,omitempty"`
		Blocking   bool     `json:"blocking"`
	}
	out := []result{}
	for _, fh := range r.MultipartForm.File {
		for _, h := range fh {
			f, err := h.Open()
			if err != nil {
				continue
			}
			dec, _, err := csvin.Decode(h.Filename, f)
			f.Close()
			if err != nil {
				out = append(out, result{File: h.Filename, Blocking: true})
				continue
			}
			d, err := csvin.Detect(dec.Header)
			if err != nil {
				out = append(out, result{File: h.Filename, Blocking: true})
				continue
			}
			out = append(out, result{
				File: h.Filename, Kind: string(d.Kind), Label: d.Kind.Label(),
				Confidence: d.Confidence, Rows: len(dec.Records), Missing: d.MissingReq,
				Blocking: len(d.MissingReq) > 0,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
	return nil
}

func (s *Server) importCommit(w http.ResponseWriter, r *http.Request) error {
	batchNo := r.PathValue("batchNo")
	var d importer.Decisions
	// body 可为空：全部走默认策略。
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeErr(w, http.StatusBadRequest, "请求体解析失败")
			return nil
		}
	}
	rep, err := s.imp.Commit(r.Context(), batchNo, d)
	if err != nil {
		writeImportErr(w, err)
		return nil
	}
	writeJSON(w, http.StatusOK, rep)
	return nil
}

func (s *Server) importRollback(w http.ResponseWriter, r *http.Request) error {
	res, err := s.imp.Rollback(r.Context(), r.PathValue("batchNo"))
	if err != nil {
		writeImportErr(w, err)
		return nil
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) importList(w http.ResponseWriter, r *http.Request) error {
	list, err := s.imp.List(r.Context(), queryLimit(r, 50, maxPageSize))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (s *Server) importDetail(w http.ResponseWriter, r *http.Request) error {
	rep, err := s.imp.Report(r.Context(), r.PathValue("batchNo"))
	if err != nil {
		if errors.Is(err, importer.ErrBatchNotFound) {
			writeErr(w, http.StatusNotFound, "批次不存在")
			return nil
		}
		return err
	}
	writeJSON(w, http.StatusOK, rep)
	return nil
}

func (s *Server) importIssues(w http.ResponseWriter, r *http.Request) error {
	var importID int64
	if err := s.st.DB.QueryRowContext(r.Context(),
		`SELECT id FROM imports WHERE batch_no=?`, r.PathValue("batchNo")).Scan(&importID); err != nil {
		writeErr(w, http.StatusNotFound, "批次不存在")
		return nil
	}
	q := `SELECT kind, line, row_no, col, field, value, severity, code, message, hint
	      FROM import_issues WHERE import_id=?`
	// 总数与列表用同一组过滤条件：前端要显示「共 N 条」并据此翻页，
	// 两个查询的过滤条件不一致会让页码算错。
	countQ := `SELECT COUNT(*) FROM import_issues WHERE import_id=?`
	args := []any{importID}
	countArgs := []any{importID}
	for _, f := range []struct{ param, column string }{
		{"severity", "severity"},
		{"code", "code"},
		{"kind", "kind"},
	} {
		if v := query(r, f.param, ""); v != "" {
			q += " AND " + f.column + "=?"
			args = append(args, v)
			countQ += " AND " + f.column + "=?"
			countArgs = append(countArgs, v)
		}
	}
	q += " ORDER BY line LIMIT ? OFFSET ?"
	args = append(args, queryLimit(r, 200, maxPageSize), queryOffset(r))

	var total int
	if err := s.st.DB.QueryRowContext(r.Context(), countQ, countArgs...).Scan(&total); err != nil {
		return err
	}

	rows, err := s.st.DB.QueryContext(r.Context(), q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	type issue struct {
		Kind     string `json:"kind"`
		Line     int    `json:"line"`
		RowNo    int    `json:"row_no"`
		Col      string `json:"col"`
		Field    string `json:"field"`
		Value    string `json:"value"`
		Severity string `json:"severity"`
		Code     string `json:"code"`
		Message  string `json:"message"`
		Hint     string `json:"hint"`
	}
	out := []issue{}
	for rows.Next() {
		var i issue
		if err := rows.Scan(&i.Kind, &i.Line, &i.RowNo, &i.Col, &i.Field,
			&i.Value, &i.Severity, &i.Code, &i.Message, &i.Hint); err != nil {
			return err
		}
		out = append(out, i)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out, "total": total})
	return nil
}

// importIssuesCSV 导出问题明细。
//
// 必须写 UTF-8 BOM：不含 BOM 的中文 CSV 在 Excel 里双击打开是乱码，而用户
// 修正数据的动作几乎一定发生在 Excel 里。
func (s *Server) importIssuesCSV(w http.ResponseWriter, r *http.Request) error {
	var importID int64
	if err := s.st.DB.QueryRowContext(r.Context(),
		`SELECT id FROM imports WHERE batch_no=?`, r.PathValue("batchNo")).Scan(&importID); err != nil {
		writeErr(w, http.StatusNotFound, "批次不存在")
		return nil
	}
	rows, err := s.st.DB.QueryContext(r.Context(), `
		SELECT kind, line, row_no, col, field, value, severity, code, message, hint
		FROM import_issues WHERE import_id=? ORDER BY line`, importID)
	if err != nil {
		return err
	}
	defer rows.Close()

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="youlv-issues-%s.csv"`, r.PathValue("batchNo")))
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"文件", "行号", "数据行", "列", "字段", "原始值", "级别", "错误码", "说明", "建议"})
	for rows.Next() {
		var kind, col, field, value, sev, code, msg, hint string
		var line, rowNo int
		if err := rows.Scan(&kind, &line, &rowNo, &col, &field, &value,
			&sev, &code, &msg, &hint); err != nil {
			return err
		}
		_ = cw.Write([]string{kind, strconv.Itoa(line), strconv.Itoa(rowNo),
			col, field, value, sev, code, msg, hint})
	}
	cw.Flush()
	return cw.Error()
}

// ---------- 小工具 ----------

func optionsFromRequest(r *http.Request) (importer.Options, error) {
	opt := importer.DefaultOptions()
	if v := query(r, "dup_strategy", ""); v != "" {
		if !inSet(v, "skip", "overwrite", "reject") {
			return opt, fmt.Errorf("未知的重复处理策略：%s", v)
		}
		opt.DupStrategy = v
	}
	if v := query(r, "weather_dup_strategy", ""); v != "" {
		if !inSet(v, "skip", "overwrite", "reject") {
			return opt, fmt.Errorf("未知的天气重复策略：%s", v)
		}
		opt.WeatherDupStrategy = v
	}
	if v := query(r, "departed_before_arrived", ""); v != "" {
		if !inSet(v, "null", "reject", "swap") {
			return opt, fmt.Errorf("未知的时间倒挂策略：%s", v)
		}
		opt.DepartedBeforeArrived = v
	}
	if r.URL.Query().Get("create_stubs") == "0" {
		opt.CreateStubs = false
	}
	return opt, nil
}

func inSet(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func nowUnix() int64 { return time.Now().Unix() }
