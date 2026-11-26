// pa_requests.go 面板：最近请求事件（跨平台）。
package panelagg

import (
	"net/http"
	"strconv"

	"github.com/misakano7545/workbuddy2api-panel/internal/agg"
)

// recentRequests 返回最近 n 条请求事件（新的在前）。
func (h *Handler) recentRequests(w http.ResponseWriter, r *http.Request) {
	n := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x > 0 && x <= 500 {
			n = x
		}
	}
	log := agg.SharedReqLog()
	if log == nil {
		writeJSON(w, http.StatusOK, map[string]any{"events": []any{}, "note": "日志未启用"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": log.Recent(n)})
}
