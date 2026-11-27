// tasks_usage_merge.go 主面板「实时任务」与「用量」页：并入扩展平台数据。
//
// 两个页面读的是上游接口，只认 WorkBuddy 的账号：
//   GET /panel/api/tasks/live  {accounts:[{uid,nickname,realm,tasks:[...]}]}
//   GET /panel/api/usage       {totals:{...}, by_realm:[...], by_account:[...]}
//
// 这里按同形字段追加扩展平台的部分：
//   - 任务：码道签到/任务由 L0NE 脚本负责，网关不参与。列出账号 + 一条说明性任务项，
//           不伪造进度（current=0，accept_status=external）。
//   - 用量：把请求日志里本平台的数据汇总后并进 by_realm / by_account / totals，
//           这样"今天用了多少 token"能看到全貌。
package agg

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

// ---------- 实时任务 ----------

func serveTasksLiveMerged(w http.ResponseWriter, next http.Handler, r *http.Request, st *creds.Store) {
	cw := &captureWriter{header: make(http.Header), code: http.StatusOK}
	next.ServeHTTP(cw, r)
	body := cw.body.Bytes()
	if cw.code == http.StatusOK && st != nil {
		if merged, ok := mergeTasksLive(body, st); ok {
			body = merged
		}
	}
	copyHeaders(w, cw.header)
	w.Header().Set("Content-Length", itoa(len(body)))
	w.WriteHeader(cw.code)
	_, _ = w.Write(body)
}

func mergeTasksLive(body []byte, st *creds.Store) ([]byte, bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false
	}
	accounts, _ := doc["accounts"].([]any)

	// WorkBuddy：只给本机脚本覆盖的账号挂"脚本签到结果"，其它账号不动
	wb := loadSignin("wb")
	signed := wbSignedUIDs()
	if len(accounts) > 0 && len(signed) > 0 {
		for i, raw := range accounts {
			a, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			uid, _ := a["uid"].(string)
			if uid == "" || !signed[firstN(uid, 8)] {
				continue
			}
			tasks, _ := a["tasks"].([]any)
			a["tasks"] = append(tasks, signinAsTask("WorkBuddy 签到（L0NE 脚本）", wb))
			accounts[i] = a
		}
	}

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
		accounts = append(accounts, map[string]any{
			"uid":      uidPrefixCodeArts + rec.UserID,
			"nickname": "码道 · " + name,
			"realm":    "cn",
			"platform": "codearts",
			"note":     "签到与任务由 L0NE 脚本负责，网关只做消费与积分",
			"tasks": []any{signinAsTask("码道签到（L0NE 脚本）", loadSignin("codearts"))},
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

// ---------- 用量 ----------

func serveUsageMerged(w http.ResponseWriter, next http.Handler, r *http.Request, log *ReqLog) {
	cw := &captureWriter{header: make(http.Header), code: http.StatusOK}
	next.ServeHTTP(cw, r)
	body := cw.body.Bytes()
	if cw.code == http.StatusOK && log != nil {
		if merged, ok := mergeUsage(body, log); ok {
			body = merged
		}
	}
	copyHeaders(w, cw.header)
	w.Header().Set("Content-Length", itoa(len(body)))
	w.WriteHeader(cw.code)
	_, _ = w.Write(body)
}

func mergeUsage(body []byte, log *ReqLog) ([]byte, bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false
	}
	events := log.Recent(1000)
	cut := time.Now().Add(-72 * time.Hour)

	type aggStat struct {
		requests, errors    int
		prompt, completion  int64
		totalMs             int64
	}
	byAccount := map[string]*aggStat{}
	total := &aggStat{}
	for _, e := range events {
		if e.Time.Before(cut) || e.Platform == "workbuddy" {
			continue
		}
		k := e.Account
		if k == "" {
			k = "未记录账号（早期请求）"
		}
		s := byAccount[k]
		if s == nil {
			s = &aggStat{}
			byAccount[k] = s
		}
		s.requests++
		if !e.OK {
			s.errors++
		}
		s.prompt += e.PromptTokens
		s.completion += e.OutputTokens
		s.totalMs += e.DurationMs
		total.requests++
		if !e.OK {
			total.errors++
		}
		total.prompt += e.PromptTokens
		total.completion += e.OutputTokens
		total.totalMs += e.DurationMs
	}
	if total.requests == 0 {
		return body, false
	}

	row := func(key, realm, extra string, s *aggStat) map[string]any {
		avg := int64(0)
		if s.requests > 0 {
			avg = s.totalMs / int64(s.requests)
		}
		return map[string]any{
			"key": key, "realm": realm, "extra": extra,
			"requests": s.requests, "errors": s.errors,
			"prompt_tokens": s.prompt, "completion_tokens": s.completion,
			"total_tokens": s.prompt + s.completion,
			"cache_hit_tokens": 0, "cache_miss_tokens": s.prompt,
			"avg_latency_ms": avg,
			"credits": 0, "credit_samples": 0, "credit_tokens": 0,
		}
	}

	// by_realm：按平台一行
	if list, ok := doc["by_realm"].([]any); ok {
		list = append(list, row("codearts", "codearts", "码道", total))
		doc["by_realm"] = list
	}
	// by_account：按账号一行
	if list, ok := doc["by_account"].([]any); ok {
		for k, s := range byAccount {
			list = append(list, row(k, "codearts", "码道", s))
		}
		doc["by_account"] = list
	}
	// totals：把我们的用量加进去（否则总计对不上明细）
	if t, ok := doc["totals"].(map[string]any); ok {
		addNum(t, "requests", float64(total.requests))
		addNum(t, "errors", float64(total.errors))
		addNum(t, "prompt_tokens", float64(total.prompt))
		addNum(t, "completion_tokens", float64(total.completion))
		addNum(t, "total_tokens", float64(total.prompt+total.completion))
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return body, false
	}
	return out, true
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func addNum(m map[string]any, key string, delta float64) {
	switch v := m[key].(type) {
	case float64:
		m[key] = v + delta
	case int64:
		m[key] = float64(v) + delta
	case nil:
		m[key] = delta
	}
}

func copyHeaders(w http.ResponseWriter, h http.Header) {
	for k, vs := range h {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
}
