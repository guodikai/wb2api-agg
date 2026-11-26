// doubao.go 面板里的豆包页：cookie 导入 + 只读探针。
package panelagg

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/doubao"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

// doubaoImportRequest 面板导入参数。
type doubaoImportRequest struct {
	Name      string `json:"name"`
	Cookie    string `json:"cookie"`
	DeviceID  string `json:"device_id"`
	WebID     string `json:"web_id"`
	FP        string `json:"fp"`
	MSToken   string `json:"ms_token"`
	PCVersion string `json:"pc_version"`
	UserAgent string `json:"user_agent"`
}

// handleDoubaoImport 粘贴 cookie + 设备参数 → 落盘。
//
// 设备参数为什么必须手填：`device_id`/`web_id` 来自真实网页客户端的 localStorage，
// 纯 HTTP 拿不到；页面会给一段 Console 脚本，粘一次即可。
func (h *Handler) handleDoubaoImport(w http.ResponseWriter, r *http.Request) {
	var req doubaoImportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", "请求体不是合法 JSON")
		return
	}
	cookies := creds.ParseCookieString(req.Cookie)
	if len(cookies) == 0 {
		writeErr(w, http.StatusBadRequest, "no_cookie", "没有解析到 cookie（格式应为 k=v; k=v）")
		return
	}
	deviceID := strings.TrimSpace(req.DeviceID)
	webID := strings.TrimSpace(req.WebID)
	fp := strings.TrimSpace(req.FP)
	msToken := strings.TrimSpace(req.MSToken)

	// 能从 cookie 里补的就补：s_v_web_id → fp，msToken → ms_token。
	if fp == "" {
		fp = cookies["s_v_web_id"]
	}
	if msToken == "" {
		msToken = cookies["msToken"]
	}

	miss := creds.MissingCookies(cookies)
	if deviceID == "" || webID == "" {
		writeErr(w, http.StatusBadRequest, "missing_device",
			"缺少 device_id / web_id（设备参数必须成套：请用页面上的 Console 脚本取）")
		return
	}
	if len(miss) > 0 {
		writeErr(w, http.StatusBadRequest, "missing_cookie",
			"cookie 不完整，缺: "+strings.Join(miss, ", "))
		return
	}

	id := doubaoAccountID(cookies)
	rec := creds.DoubaoRecord{
		ID: id, Name: strings.TrimSpace(req.Name), Cookies: cookies,
		DeviceID: deviceID, WebID: webID, FP: fp, MSToken: msToken,
		PCVersion: strings.TrimSpace(req.PCVersion), UserAgent: strings.TrimSpace(req.UserAgent),
		UpdatedAt: h.cfg.Now().Format(time.RFC3339),
	}
	if err := h.cfg.Store.Save(creds.PlatformDoubao, id, rec); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "id": id, "name": rec.Name,
		"device_id": deviceID, "web_id": webID, "fp_set": fp != "", "ms_token_set": msToken != "",
	})
}

// doubaoAccountID 用 uid_tt（或 sessionid 前段）派生稳定 id，避免每次导入生成新账号。
func doubaoAccountID(cookies map[string]string) string {
	seed := cookies["uid_tt"]
	if seed == "" {
		seed = cookies["uid_tt_ss"]
	}
	if seed == "" {
		seed = cookies["sessionid"]
	}
	sum := sha1.Sum([]byte(seed))
	return "doubao-" + hex.EncodeToString(sum[:])[:16]
}

// handleDoubaoProbe 只读探针：拉额度汇总（不消耗额度）。
func (h *Handler) handleDoubaoProbe(w http.ResponseWriter, r *http.Request) {
	ids, err := h.cfg.Store.List(creds.PlatformDoubao)
	if err != nil || len(ids) == 0 {
		writeErr(w, http.StatusNotFound, "no_account", "还没有导入豆包账号")
		return
	}
	var rec creds.DoubaoRecord
	if err := h.cfg.Store.Load(creds.PlatformDoubao, ids[0], &rec); err != nil {
		writeErr(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	client := doubao.New(h.doubaoBase())
	raw, err := client.QuotaSummary(r.Context(), rec.Account())
	if err != nil {
		if ue, ok := err.(*doubao.UpstreamError); ok {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"ok": false, "account": ids[0], "status": ue.Status, "code": ue.Code,
				"verification_required": ue.IsVerification(), "message": ue.Body,
			})
			return
		}
		writeErr(w, http.StatusBadGateway, "probe_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "account": ids[0], "bytes": len(raw), "sample": trimJSON(raw, 400),
	})
}

// doubaoBase 允许测试注入假上游。
func (h *Handler) doubaoBase() string {
	if h.cfg.DoubaoBase != "" {
		return h.cfg.DoubaoBase
	}
	return ""
}

// trimJSON 取前 n 字节做展示（不打印整包，避免把 cookie 带出去）。
func trimJSON(b []byte, n int) string {
	s := string(b)
	if len(s) > n {
		s = s[:n] + "…"
	}
	return s
}
