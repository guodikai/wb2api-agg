// credits_history.go 面板：余额趋势数据。
package panelagg

import (
	"net/http"
	"strconv"
)

// creditsHistory 返回最近 N 小时的积分样本（按时间升序）。
func (h *Handler) creditsHistory(w http.ResponseWriter, r *http.Request) {
	hours := 72
	if v := r.URL.Query().Get("hours"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x > 0 && x <= 720 {
			hours = x
		}
	}
	platform := r.URL.Query().Get("platform")
	if h.cfg.CreditSamples == nil {
		writeJSON(w, http.StatusOK, map[string]any{"samples": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"samples": h.cfg.CreditSamples(hours, platform)})
}
