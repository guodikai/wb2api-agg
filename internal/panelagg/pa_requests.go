// pa_requests.go 面板：最近请求事件（数据由 agg 注入，避免包循环依赖）。
package panelagg

import (
	"net/http"
	"strconv"
)

// recentRequests 返回最近 n 条请求事件（新的在前）。
func (h *Handler) recentRequests(w http.ResponseWriter, r *http.Request) {
	n := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x > 0 && x <= 500 {
			n = x
		}
	}
	if h.cfg.RecentRequests == nil {
		writeJSON(w, http.StatusOK, map[string]any{"events": []any{}, "note": "日志未启用"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": h.cfg.RecentRequests(n)})
}
