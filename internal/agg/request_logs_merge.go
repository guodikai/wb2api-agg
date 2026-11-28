// request_logs_merge.go 把扩展平台的请求事件合并进主面板「运行日志/请求记录」。
//
// 主面板读 /panel/api/request_logs（上游 reqlog 归档，只含 WorkBuddy）。这里拦截该接口，
// 把我们的 agg-requests-*.jsonl 按上游同形字段映射后追加，再按时间倒序截断——
// 这样一张表里同时能看到两个平台的请求，不需要用户去另一个页签翻。
package agg

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// toUpstreamEntry 把一个我们的 ReqEvent 映射成上游 reqlog.Event 的 JSON 形状。
func toUpstreamEntry(e ReqEvent) map[string]any {
	realm := e.Platform
	account := e.Platform
	if e.Account != "" {
		account = e.Platform + " · " + e.Account
	}
	// 前端只认 success / http_error / stream_error / interrupted，
	// 其它字符串一律按异常标红（我们自己的 sync/stream 就会被显示成红色 200）。
	outcome := "success"
	if !e.OK {
		if strings.Contains(e.Outcome, "stream") {
			outcome = "stream_error"
		} else {
			outcome = "http_error"
		}
	}
	m := map[string]any{
		"time":     e.Time.Format(time.RFC3339Nano),
		"path":     "/v1/chat/completions",
		"account":  account,
		"model":    e.ModelRaw,
		"realm":    realm,
		"status":   e.Status,
		"ok":       e.OK,
		"outcome":  outcome,
		"duration_ms": e.DurationMs,
		"credit_known": false,
	}
	if e.Model != "" && e.Model != e.ModelRaw {
		m["model_eff"] = e.Model
	}
	if e.PromptTokens > 0 {
		m["prompt_tokens"] = e.PromptTokens
	}
	if e.OutputTokens > 0 {
		m["completion_tokens"] = e.OutputTokens
	}
	if e.PromptTokens > 0 || e.OutputTokens > 0 {
		m["total_tokens"] = e.PromptTokens + e.OutputTokens
	}
	if e.ClientIP != "" {
		m["client_ip"] = e.ClientIP
	}
	if e.Err != "" {
		m["error"] = e.Err
	}
	return m
}

// serveRequestLogsMerged 捕获上游 request_logs → 合并我们的事件 → 透传。
func serveRequestLogsMerged(w http.ResponseWriter, r *http.Request, next http.Handler, log *ReqLog) {
	cw := &captureWriter{header: make(http.Header), code: http.StatusOK}
	next.ServeHTTP(cw, r)
	body := cw.body.Bytes()

	if cw.code == http.StatusOK && log != nil {
		if merged, ok := mergeRequestLogs(body, r, log); ok {
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
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(cw.code)
	_, _ = w.Write(body)
}

func mergeRequestLogs(body []byte, r *http.Request, log *ReqLog) ([]byte, bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false
	}
	entries, _ := doc["entries"].([]any)

	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}

	ours := log.Recent(limit)
	if len(ours) == 0 {
		return body, false
	}

	// 按上游的筛选参数过滤我们的记录（model / account / client_ip / outcome）
	// 跳过 workbuddy：那部分上游 reqlog 已经记了，合并会重复一行。
	q := r.URL.Query()
	for _, e := range ours {
		if e.Platform == "workbuddy" {
			continue
		}
		if mv := q.Get("model"); mv != "" && !strings.Contains(strings.ToLower(e.ModelRaw), strings.ToLower(mv)) {
			continue
		}
		if av := q.Get("account"); av != "" && !strings.Contains(strings.ToLower(e.Platform), strings.ToLower(av)) {
			continue
		}
		if cv := q.Get("client_ip"); cv != "" && !strings.Contains(e.ClientIP, cv) {
			continue
		}
		if ov := q.Get("outcome"); ov != "" && !strings.Contains(strings.ToLower(e.Outcome), strings.ToLower(ov)) {
			continue
		}
		entries = append(entries, toUpstreamEntry(e))
	}

	sort.SliceStable(entries, func(i, j int) bool {
		ti, _ := entries[i].(map[string]any)["time"].(string)
		tj, _ := entries[j].(map[string]any)["time"].(string)
		return ti > tj
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	doc["entries"] = entries
	out, err := json.Marshal(doc)
	if err != nil {
		return body, false
	}
	return out, true
}
