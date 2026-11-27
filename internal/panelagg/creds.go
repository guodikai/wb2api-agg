// pa_creds.go 面板：给签到脚本提供短效 STS。
//
// 为什么需要：码道的 refresh_token 是**一次性轮换**的，只能有一个写者。
// 消费侧（网关）独占它；签到脚本自己去刷新会和网关互相踢下线。
// 所以脚本每次运行前调本接口拿一份 AK|SK|STS（1 小时有效、只读），
// 全程不接触 refresh_token；STS 由网关的 CredentialSource 保证新鲜。
package panelagg

import (
	"net/http"
)

// codeartsCredentials GET /panel/agg/api/codearts/credentials?account=<id>
// 返回 {"account":"...","credential":"AK|SK|STS"}。
func (h *Handler) codeartsCredentials(w http.ResponseWriter, r *http.Request) {
	if h.cfg.CodeArtsCredential == nil {
		writeErr(w, http.StatusNotImplemented, "not_available", "凭据导出未启用")
		return
	}
	id := r.URL.Query().Get("account")
	cred, err := h.cfg.CodeArtsCredential(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "credential_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": id, "credential": cred})
}
