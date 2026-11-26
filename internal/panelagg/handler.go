// handler.go 新底座自己的小面板：/panel/agg/
//
// 为什么单独一页：misakano 的面板只认识它自己的账号存储（auth_dir），
// 码道账号存在我们的 creds 目录里，硬塞进它的数据模型会把上游代码改烂。
// 所以这页独立存在，等 P5 再决定要不要接进主面板导航。
package panelagg

import (
	"crypto/rand"
	"encoding/base64"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

//go:embed page.html
var pageHTML []byte

// Config 面板依赖。
type Config struct {
	APIKey string
	Store  *creds.Store
	Client *codearts.Client
	Login  codearts.LoginConfig
	Now    func() time.Time
}

// Handler 面板请求分发。
type Handler struct {
	cfg Config

	mu      sync.Mutex
	pending map[string]*pendingLogin // ticket → 待完成登录
}

type pendingLogin struct {
	verifier  string
	jwk       codearts.DPoPPrivateJWK
	port      int
	createdAt time.Time
}

// New 构造。
func New(cfg Config) *Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Handler{cfg: cfg, pending: make(map[string]*pendingLogin)}
}

// ServeHTTP 处理 /panel/agg/ 下的全部请求。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/panel/agg" {
		http.Redirect(w, r, "/panel/agg/", http.StatusFound)
		return
	}
	// 页面本身不需要鉴权（内容为空壳），所有数据接口都要 api_key。
	if r.URL.Path == "/panel/agg/" && r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(pageHTML)
		return
	}
	if !h.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "需要 api_key（?key= 或 Authorization: Bearer）")
		return
	}

	switch {
	case r.URL.Path == "/panel/agg/api/overview" && r.Method == http.MethodGet:
		h.overview(w, r)
	case r.URL.Path == "/panel/agg/api/codearts/login" && r.Method == http.MethodPost:
		h.startLogin(w, r)
	case r.URL.Path == "/panel/agg/api/codearts/models" && r.Method == http.MethodGet:
		h.codeartsModels(w, r)
	case r.URL.Path == "/panel/agg/api/codearts/complete" && r.Method == http.MethodPost:
		h.completeLogin(w, r)
	default:
		writeErr(w, http.StatusNotFound, "not_found", "未知路径")
	}
}

func (h *Handler) authorized(r *http.Request) bool {
	if h.cfg.APIKey == "" {
		return true
	}
	if k := r.URL.Query().Get("key"); k == h.cfg.APIKey {
		return true
	}
	auth := r.Header.Get("Authorization")
	return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer ")) == h.cfg.APIKey
}

// overview 返回各平台账号概览。
func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	type acct struct {
		ID        string `json:"id"`
		User      string `json:"user,omitempty"`
		Expires   string `json:"sts_expires,omitempty"`
		UpdatedAt string `json:"updated_at,omitempty"`
		State     string `json:"state"`
	}
	out := struct {
		Platforms []string `json:"platforms"`
		CodeArts  []acct   `json:"codearts"`
	}{Platforms: []string{"workbuddy", "codearts", "doubao"}}

	ids, err := h.cfg.Store.List(creds.PlatformCodeArts)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store_error", err.Error())
		return
	}
	for _, id := range ids {
		var rec creds.CodeArtsRecord
		a := acct{ID: id}
		if err := h.cfg.Store.Load(creds.PlatformCodeArts, id, &rec); err != nil {
			a.State = "unreadable"
			out.CodeArts = append(out.CodeArts, a)
			continue
		}
		a.User = rec.UserName
		a.Expires = rec.Expiration
		a.UpdatedAt = rec.UpdatedAt
		switch {
		case rec.SecurityToken == "" || rec.AccessKeyID == "":
			a.State = "need_login"
		case rec.RefreshTok == "" || rec.DPoPJWK == nil:
			a.State = "need_relogin"
		default:
			a.State = "ok"
		}
		out.CodeArts = append(out.CodeArts, a)
	}
	writeJSON(w, http.StatusOK, out)
}

// codeartsModels 用第一个可用账号拉上游模型目录（诊断用：确认能调哪些 id）。
func (h *Handler) codeartsModels(w http.ResponseWriter, r *http.Request) {
	ids, err := h.cfg.Store.List(creds.PlatformCodeArts)
	if err != nil || len(ids) == 0 {
		writeErr(w, http.StatusNotFound, "no_account", "还没有码道账号")
		return
	}
	var rec creds.CodeArtsRecord
	if err := h.cfg.Store.Load(creds.PlatformCodeArts, ids[0], &rec); err != nil {
		writeErr(w, http.StatusInternalServerError, "load_failed", err.Error())
		return
	}
	models, err := h.cfg.Client.FetchModels(rec.Auth())
	if err != nil {
		writeErr(w, http.StatusBadGateway, "fetch_failed", err.Error())
		return
	}
	list := make([]map[string]any, 0, len(models))
	for _, m := range models {
		list = append(list, map[string]any{
			"id": m.ID, "name": m.Name, "context_window": m.ContextWindow, "max_tokens": m.MaxTokens,
			"benefit": m.Benefit,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": ids[0], "count": len(list), "models": list})
}

// startLogin 生成授权链接（PKCE + ticket），把私钥留在内存等回调。
func (h *Handler) startLogin(w http.ResponseWriter, r *http.Request) {
	pkce, err := codearts.NewPKCE()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "pkce_error", err.Error())
		return
	}
	jwk, err := codearts.NewDPoPPrivateJWK()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "dpop_error", err.Error())
		return
	}
	ticket := randomHex(16)
	const port = 18234 // 仅用于拼 redirect_uri；我们不会真的监听它
	h.mu.Lock()
	h.pending[ticket] = &pendingLogin{verifier: pkce.Verifier, jwk: jwk, port: port, createdAt: h.cfg.Now()}
	// 清理超过 15 分钟的挂起登录，避免内存堆积
	for k, p := range h.pending {
		if h.cfg.Now().Sub(p.createdAt) > 15*time.Minute {
			delete(h.pending, k)
		}
	}
	h.mu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"ticket":    ticket,
		"login_url": h.cfg.Client.BuildAuthorizeURL(h.cfg.Login, ticket, pkce.Challenge, pkce.Method, port),
		"hint":      "浏览器打开 login_url 完成授权；把跳转后（打不开）的地址整条粘贴到下方完成登录",
	})
}

// completeLogin 用粘贴回来的回调地址完成换取，并落盘凭据。
func (h *Handler) completeLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ticket      string `json:"ticket"`
		CallbackURL string `json:"callback_url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_json", "请求体不是合法 JSON")
		return
	}
	body.Ticket = strings.TrimSpace(body.Ticket)
	p, ok := h.takePending(body.Ticket)
	if !ok {
		writeErr(w, http.StatusNotFound, "no_pending", "登录会话不存在或已过期，请重新生成链接")
		return
	}

	u, err := url.Parse(strings.TrimSpace(body.CallbackURL))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_callback", "回调地址解析失败")
		return
	}
	code := u.Query().Get("code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "no_code", "回调地址里没有 code 参数")
		return
	}

	out, err := h.cfg.Client.ExchangeCode(r.Context(), h.cfg.Login, code, p.verifier, p.port, p.jwk)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "exchange_failed", err.Error())
		return
	}
	userID := out.UserID
	userName := out.UserName
	domainID := out.DomainID

	// 换码响应没有身份时，按参照实现的三级兜底：caller-identity → current/user → RT 的 sub
	if userID == "" || userName == "" {
		cred := codearts.SignCredential{
			AccessKeyID:     out.Credentials.AccessKeyID,
			SecretAccessKey: out.Credentials.SecretAccessKey,
			SecurityToken:   out.Credentials.SecurityToken,
		}
		if uid, name, dom, err := codearts.CallerIdentity(cred); err == nil && uid != "" {
			userID, userName, domainID = uid, name, dom
		} else if uid, name, dom, err := codearts.CurrentUser(cred); err == nil && uid != "" {
			userID, userName, domainID = uid, name, dom
		} else if sub := identityFromRefreshToken(out.RefreshToken); sub != "" {
			userID = sub
		} else {
			writeErrf(w, http.StatusBadGateway, "no_identity",
				"换取成功但无法确定账号身份（换码字段：%s）", strings.Join(topLevelKeys(out.RawJSON), ", "))
			return
		}
	}
	if userID == "" {
		writeErrf(w, http.StatusBadGateway, "no_user_id",
			"上游没有返回可用的用户标识（换码字段：%s）", strings.Join(topLevelKeys(out.RawJSON), ", "))
		return
	}

	rec := creds.CodeArtsRecord{
		UserID: userID, UserName: userName, DomainID: domainID,
		RefreshTok: out.RefreshToken, Verifier: p.verifier,
		DPoPJWK:         map[string]string(p.jwk),
		AccessKeyID:     out.Credentials.AccessKeyID,
		SecretAccessKey: out.Credentials.SecretAccessKey,
		SecurityToken:   out.Credentials.SecurityToken,
		Expiration:      out.Credentials.Expiration,
		UpdatedAt:       h.cfg.Now().Format(time.RFC3339),
	}
	if err := h.cfg.Store.Save(creds.PlatformCodeArts, userID, rec); err != nil {
		writeErr(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user_id": userID, "user_name": userName})
}

func (h *Handler) takePending(ticket string) (*pendingLogin, bool) {
	if ticket == "" {
		return nil, false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.pending[ticket]
	if !ok {
		return nil, false
	}
	delete(h.pending, ticket) // 一次性：失败也让用户重来，避免半状态
	if h.cfg.Now().Sub(p.createdAt) > 15*time.Minute {
		return nil, false
	}
	return p, true
}

// extractUserID 兼容几种可能的身份字段位置。
func extractUserID(out *codearts.TokenResponse) string {
	if v := strings.TrimSpace(out.UserID); v != "" {
		return v
	}
	// 兜底：从原始 JSON 里按常见路径找
	var m map[string]any
	if json.Unmarshal(out.RawJSON, &m) != nil {
		return ""
	}
	pick := func(v any) string {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	for _, k := range []string{"user_id", "userId", "uid", "account_id", "accountId", "subject", "sub"} {
		if v := pick(m[k]); v != "" {
			return v
		}
	}
	for _, obj := range []string{"user", "principal", "account", "profile"} {
		if sub, ok := m[obj].(map[string]any); ok {
			for _, k := range []string{"id", "user_id", "userId", "uid", "name"} {
				if v := pick(sub[k]); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// pickID / pickName 从任意层级的身份对象里取 id 与显示名。
func pickID(m map[string]any) string {
	for _, k := range []string{"user_id", "userId", "userID", "id", "uid", "account_id", "accountId", "subject", "sub", "principal_id"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, k := range []string{"user", "principal", "account", "profile", "identity"} {
		if sub, ok := m[k].(map[string]any); ok {
			if v := pickID(sub); v != "" {
				return v
			}
		}
	}
	return ""
}

func pickName(m map[string]any) string {
	for _, k := range []string{"user_name", "userName", "username", "name", "display_name", "displayName", "account", "email"} {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, k := range []string{"user", "principal", "account", "profile", "identity"} {
		if sub, ok := m[k].(map[string]any); ok {
			if v := pickName(sub); v != "" {
				return v
			}
		}
	}
	return ""
}

// identityFromRefreshToken 从 JWT 形式的 refresh_token 里取 sub（照抄参照实现，
// 作为身份兜底的最后一级，纯本地无网络）。
func identityFromRefreshToken(tok string) string {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var payload struct {
		Sub string `json:"sub"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return ""
	}
	return strings.TrimSpace(payload.Sub)
}

// topLevelKeys 返回顶层字段名（排序），用于诊断。
func topLevelKeys(raw []byte) []string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

// writeErrf 格式化版本（仅用于常量格式串，避免 vet 的非常量格式警告）。
func writeErrf(w http.ResponseWriter, status int, code, format string, args ...any) {
	writeErr(w, status, code, fmt.Sprintf(format, args...))
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}
