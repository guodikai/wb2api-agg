// packages_merge.go 主面板「积分构成」页：追加扩展平台的积分构成。
//
// 上游这个页面逐账号向上游查"逐包明细"，只认自己的账号。我们按同样的字段形状
// 追加一行，用 statistics/plugin 的 metrics 折成"包"（码道不给逐包明细，
// 给的是各口径的总额，所以按口径各算一个包，名字标明口径）。
package agg

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

func servePackagesWithPlatforms(w http.ResponseWriter, next http.Handler, r *http.Request, st *creds.Store) {
	cw := &captureWriter{header: make(http.Header), code: http.StatusOK}
	next.ServeHTTP(cw, r)
	body := cw.body.Bytes()

	if cw.code == http.StatusOK && st != nil {
		if merged, ok := mergePackages(body, st); ok {
			body = merged
		}
	}

	for k, vs := range cw.header {
		if strings.EqualFold(k, "Content-Length") {
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

func mergePackages(body []byte, st *creds.Store) ([]byte, bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false
	}
	accounts, _ := doc["accounts"].([]any)

	ids, err := st.List(creds.PlatformCodeArts)
	if err != nil || len(ids) == 0 {
		return body, false
	}

	added := 0
	for _, id := range ids {
		var rec creds.CodeArtsRecord
		if err := st.Load(creds.PlatformCodeArts, id, &rec); err != nil {
			continue
		}
		name := rec.UserName
		if name == "" {
			name = id
		}
		// 按口径折成"包"：remain 用各口径剩余，used 用总额-剩余（只有总量口径可靠）
		var packs []any
		keys := make([]string, 0, len(rec.CreditsBreakdown))
		for k := range rec.CreditsBreakdown {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := rec.CreditsBreakdown[k]
			p := map[string]any{
				"name":   k + "（码道）",
				"remain": v,
				"size":   v,
				"used":   0,
			}
			if rec.CreditsPeriod != "" {
				p["end_time"] = rec.CreditsPeriod
			}
			packs = append(packs, p)
		}
		if packs == nil {
			packs = []any{}
		}
		accounts = append(accounts, map[string]any{
			"uid":      uidPrefixCodeArts + rec.UserID,
			"nickname": "码道 · " + name,
			"realm":    "cn",
			"platform": "codearts",
			"remain":   rec.Credits,
			"size":     rec.CreditsTotal,
			"packages": packs,
		})
		added++
	}
	if added == 0 {
		return body, false
	}
	doc["accounts"] = accounts
	out, err := json.Marshal(doc)
	if err != nil {
		return body, false
	}
	return out, true
}
