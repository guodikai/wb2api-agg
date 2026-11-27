// overview_merge.go 把扩展平台的账号合并进主面板的「账号池」。
//
// 原理：主面板的账号列表来自 GET /panel/api/overview（上游自己的接口）。我们在这里
// 拦截响应，把码道等平台的账号按同一套字段形状追加进去——面板不用改一行 app.js 就会
// 原生渲染出来（表格、状态色、统计数字全部照旧）。
//
// 字段形状以 2026-11-26 实测的上游响应为准；类型不匹配会让 app.js 渲染成 undefined，
// 所以这里对每个字段都显式给默认值。
package agg

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

// uidPrefixCodeArts 码道账号在面板里的 uid 前缀（避免与 WorkBuddy 的 uuid 冲突）。
const uidPrefixCodeArts = "codearts:"

// platformAccount 构造一个与上游 overview 完全同形的账号对象。
func platformAccount(uid, nickname, realm string, extra map[string]any) map[string]any {
	a := map[string]any{
		"uid":                        uid,
		"nickname":                   nickname,
		"credits":                    int64(0),
		"credits_total":              int64(0),
		"credits_raw":                int64(0),
		"session_dead_fails":         0,
		"credits_expiring":           int64(0),
		"credits_earliest_expiry":    "",
		"credits_earliest_remaining": int64(0),
		"credits_expiry_buckets":     []any{},
		"cooling":                    false,
		"until":                      "",
		"realm":                      realm,
		"disabled":                   false,
		"success_count":              int64(0),
		"last_success":               "",
		"last_err":                   "",
		"token_usage":                map[string]any{},
		"consecutive_fails":          0,
		"degrade_until":              "",
		"in_flight":                  0,
		"breaker_fails":              0,
		"breaker_until":              "",
	}
	for k, v := range extra {
		a[k] = v
	}
	return a
}

// collectPlatformAccounts 从凭据目录读出各扩展平台的账号（面板展示用）。
func collectPlatformAccounts(st *creds.Store) []map[string]any {
	var out []map[string]any
	if st == nil {
		return out
	}

	// 码道
	if ids, err := st.List(creds.PlatformCodeArts); err == nil {
		for _, id := range ids {
			var rec creds.CodeArtsRecord
			if err := st.Load(creds.PlatformCodeArts, id, &rec); err != nil {
				continue
			}
			name := rec.UserName
			if name == "" {
				name = id
			}
			state := "正常"
			disabled := false
			var lastErr string
			switch {
			case rec.SecurityToken == "" || rec.AccessKeyID == "":
				state, disabled, lastErr = "待登录", true, "缺少 STS 凭据"
			case rec.RefreshTok == "" || rec.DPoPJWK == nil:
				state, disabled, lastErr = "需重新登录", true, "缺少 refresh_token/DPoP 私钥"
			}
			out = append(out, platformAccount(
				uidPrefixCodeArts+rec.UserID,
				"码道 · "+name,
				"cn",
				map[string]any{
					"disabled":                   disabled,
					"last_err":                   lastErr,
					"platform":                   "codearts",
					"state":                      state,
					"platform_expiry":            rec.Expiration,
					"credits":                    int64(rec.Credits),
					"credits_total":              int64(rec.CreditsTotal),
					"credits_raw":                int64(rec.Credits),
					"credits_used":               int64(rec.CreditsUsed),
					"credits_expiring":           int64(rec.CreditsExpiring),
					"credits_earliest_expiry":    creditsExpiryOf(rec),
					"credits_breakdown":          rec.CreditsBreakdown,
				},
			))
		}
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i]["uid"].(string) < out[j]["uid"].(string)
	})
	return out
}

// mergeOverviewAccounts 在 overview 响应里追加扩展平台账号，并同步修正统计数字。
func mergeOverviewAccounts(body []byte, st *creds.Store) ([]byte, bool) {
	extra := collectPlatformAccounts(st)
	if len(extra) == 0 {
		return body, false
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false
	}
	accounts, _ := doc["accounts"].([]any)
	accounts = append(accounts, toAnySlice(extra)...)
	doc["accounts"] = accounts

	bump := func(key string, n int) {
		switch v := doc[key].(type) {
		case float64:
			doc[key] = int64(v) + int64(n)
		case int64:
			doc[key] = v + int64(n)
		}
	}
	healthy := 0
	for _, a := range extra {
		if !a["disabled"].(bool) {
			healthy++
		}
	}
	bump("total", len(extra))
	bump("healthy", healthy)

	out, err := json.Marshal(doc)
	if err != nil {
		return body, false
	}
	return out, true
}

// creditsExpiryOf 码道没有"分批到期"，用套餐周期结束日代替（面板会显示成到期时间）。
func creditsExpiryOf(rec creds.CodeArtsRecord) string {
	parts := strings.SplitN(rec.CreditsPeriod, "~", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func toAnySlice(in []map[string]any) []any {
	out := make([]any, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}

// serveOverviewWithPlatforms 捕获上游 overview → 合并 → 透传。
// 任何异常都原样回退，绝不因为扩展平台把面板弄坏。
func serveOverviewWithPlatforms(w http.ResponseWriter, next http.Handler, r *http.Request, st *creds.Store) {
	cw := &captureWriter{header: make(http.Header), code: http.StatusOK}
	next.ServeHTTP(cw, r)
	body := cw.body.Bytes()

	if cw.code == http.StatusOK && st != nil {
		if merged, ok := mergeOverviewAccounts(body, st); ok {
			body = merged
		}
	}

	for k, vs := range cw.header {
		if bytes.EqualFold([]byte(k), []byte("Content-Length")) {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Length", itoa(len(body)))
	w.WriteHeader(cw.code)
	_, _ = w.Write(body)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
