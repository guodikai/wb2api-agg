// pa_accounts.go 面板：码道账号的启用/禁用、强制刷新、删除。
package panelagg

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

type accountAction struct {
	Account  string `json:"account"`
	Disabled bool   `json:"disabled"`
}

// codeartsToggle POST /panel/agg/api/accounts/toggle   {"account":"<id>","disabled":true}
func (h *Handler) codeartsToggle(w http.ResponseWriter, r *http.Request) {
	var req accountAction
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Account == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 account")
		return
	}
	var rec creds.CodeArtsRecord
	if err := h.cfg.Store.Load(creds.PlatformCodeArts, req.Account, &rec); err != nil {
		writeErr(w, http.StatusNotFound, "account_not_found", err.Error())
		return
	}
	rec.Disabled = req.Disabled
	if err := h.cfg.Store.Save(creds.PlatformCodeArts, req.Account, &rec); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account": req.Account, "disabled": rec.Disabled})
}

// codeartsDelete POST /panel/agg/api/accounts/delete {"account":"<id>"}
// 只删凭据文件；签到脚本会因为找不到账号而跳过（面板会显示）。
func (h *Handler) codeartsDelete(w http.ResponseWriter, r *http.Request) {
	var req accountAction
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Account == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 account")
		return
	}
	if err := h.cfg.Store.Delete(creds.PlatformCodeArts, req.Account); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": req.Account})
}

// codeartsRefresh POST /panel/agg/api/accounts/refresh {"account":"<id>"}
// 强制走一次凭据刷新 + 积分刷新（用于"余额不对/凭据临期"时手动拉一把）。
func (h *Handler) codeartsRefresh(w http.ResponseWriter, r *http.Request) {
	var req accountAction
	if json.NewDecoder(r.Body).Decode(&req) != nil || req.Account == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "缺少 account")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	cred, err := h.cfg.CodeArtsCredential(ctx, req.Account)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "credential_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_len": len(cred)})
}
