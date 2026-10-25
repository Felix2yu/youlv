package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/Felix2yu/youlv/internal/importer"
	"github.com/Felix2yu/youlv/internal/store"
)

// 本文件是详情类与设置类端点的 handler。
//
// 与 handlers.go 分开不是因为它们不重要，而是它们共用一组「配置型」
// 的语义（白名单键、409 与 404 的区分、部分更新），放在一起更好维护。

// visitDetail 返回一次到访的完整档案。
func (s *Server) visitDetail(w http.ResponseWriter, r *http.Request) error {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "到访 ID 不是正整数")
		return nil
	}
	d, err := s.st.Visit(r.Context(), id, query(r, "frame", "wgs84"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "到访不存在")
		return nil
	}
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, d)
	return nil
}

// tags 返回标签字典及各自的使用次数。
func (s *Server) tags(w http.ResponseWriter, r *http.Request) error {
	list, total, err := s.st.Tags(r.Context(), queryLimit(r, 100, maxPageSize), queryOffset(r))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": list, "total": total})
	return nil
}

// placesStats 返回地点库概览。
func (s *Server) placesStats(w http.ResponseWriter, r *http.Request) error {
	st, err := s.st.PlaceStats(r.Context(), query(r, "city", ""), query(r, "origin", ""))
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, st)
	return nil
}

// mapTiles 返回底图瓦片配置。
//
// 只返回配置，不代理瓦片：代理意味着服务端主动出网，还会引入带宽、
// 缓存与 SSRF 一堆问题。自托管用户真正需要的是「指向自己的瓦片服务器」，
// 所以这里给的是可配置的 URL 模板。
func (s *Server) mapTiles(w http.ResponseWriter, r *http.Request) error {
	set, err := s.st.Settings(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url_template": set.Map.TilesURL,
		"attribution":  set.Map.TilesAttribution,
		"max_zoom":     set.Map.TilesMaxZoom,
		"subdomains":   set.Map.TilesSubdomains,
		"frame":        set.Map.Frame,
	})
	return nil
}

// settings 读取应用设置。
func (s *Server) settings(w http.ResponseWriter, r *http.Request) error {
	set, err := s.st.Settings(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, set)
	return nil
}

// settingsUpdate 更新应用设置（部分更新）。
//
// 未知字段直接 400：打错一个键名却静默不生效，是最难查的一类问题。
func (s *Server) settingsUpdate(w http.ResponseWriter, r *http.Request) error {
	var p store.SettingsPatch
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "设置项无法识别："+err.Error())
		return nil
	}
	set, err := s.st.SaveSettings(r.Context(), p)
	if err != nil {
		// SaveSettings 返回的都是校验错误（未知坐标系、越界的层级）。
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil
	}
	writeJSON(w, http.StatusOK, set)
	return nil
}

// importPurge 清空全部导入数据与批次记录。
//
// 这是本服务里唯一不可撤销的操作，所以要求请求体显式带 confirm 标记：
// 误点、误调用（curl 少写了 URL）都不至于把用户的数据抹掉。
func (s *Server) importPurge(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Confirm != purgeConfirm {
		writeErr(w, http.StatusBadRequest, "缺少确认标记，清空操作未执行")
		return nil
	}
	res, err := s.imp.Purge(r.Context())
	if err != nil {
		log.Printf("[api] 清空数据失败: %v", err)
		writeErr(w, http.StatusInternalServerError, "清空失败，详情见服务端日志")
		return nil
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

// purgeConfirm 是清空操作要求的确认值，由前端原样回传。
const purgeConfirm = "PURGE"

// importDelete 删除一个批次的审计记录。
//
// 与 rollback 的分工：rollback 删业务数据，delete 删审计行。
// 已成功导入的批次返回 409 —— 必须先撤销，否则那批数据再也回滚不掉。
func (s *Server) importDelete(w http.ResponseWriter, r *http.Request) error {
	batchNo := r.PathValue("batchNo")
	if err := s.imp.DeleteBatch(r.Context(), batchNo); err != nil {
		if errors.Is(err, importer.ErrBatchHasData) {
			writeErr(w, http.StatusConflict, err.Error())
			return nil
		}
		writeImportErr(w, err)
		return nil
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
