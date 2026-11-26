// reqlog.go 我们自己的请求事件日志（跨平台）。
//
// 为什么不用上游的 internal/reqlog：那个埋点在上游 handler 里，只覆盖 WorkBuddy；
// 我们的请求走 agg.Mux，绕过了它。共用它的 Event 结构可以，但把 recorder 接进
// 上游要改它的文件（冲突面变大），所以这里写一份独立的 JSONL：
//
//	/opt/wb2api-agg/data/agg-requests-YYYY-MM-DD.jsonl
//
// 保留 7 天，逐行 JSON，便于 grep / 面板读取 / 后续接用量统计。
package agg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// ReqEvent 一次请求的事件（字段对齐上游 reqlog 的语义，另加平台/档位）。
type ReqEvent struct {
	Time         time.Time `json:"time"`
	RequestID    string    `json:"request_id,omitempty"`
	Platform     string    `json:"platform"`
	Model        string    `json:"model,omitempty"`
	ModelEff     string    `json:"model_eff,omitempty"`
	ModelRaw     string    `json:"model_raw,omitempty"`
	Account      string    `json:"account,omitempty"`
	Stream       bool      `json:"stream"`
	Status       int       `json:"status"`
	OK           bool      `json:"ok"`
	Outcome      string    `json:"outcome,omitempty"`
	DurationMs   int64     `json:"duration_ms"`
	TTFBMs       int64     `json:"ttfb_ms,omitempty"`
	PromptTokens int64     `json:"prompt_tokens,omitempty"`
	OutputTokens int64     `json:"output_tokens,omitempty"`
	BytesOut     int64     `json:"bytes_out,omitempty"`
	ClientIP     string    `json:"client_ip,omitempty"`
	Effort       string    `json:"effort,omitempty"`
	Err          string    `json:"err,omitempty"`
}

// ReqLog 按天切分的 JSONL 记录器（并发安全）。
type ReqLog struct {
	dir     string
	mu      sync.Mutex
	curDay  string
	file    *os.File
	keepDay int
}

// NewReqLog 打开日志目录（不存在则创建）。
func NewReqLog(dir string) (*ReqLog, error) {
	if dir == "" {
		dir = "/opt/wb2api-agg/data"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &ReqLog{dir: dir, keepDay: 7}, nil
}

func (l *ReqLog) pathOf(day string) string {
	return filepath.Join(l.dir, "agg-requests-"+day+".jsonl")
}

// Record 写一行。任何错误都静默丢弃——日志不能影响请求。
func (l *ReqLog) Record(e ReqEvent) {
	if l == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	day := e.Time.Format("2006-01-02")
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	b = append(b, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil || l.curDay != day {
		if l.file != nil {
			_ = l.file.Close()
			l.file = nil
		}
		f, err := os.OpenFile(l.pathOf(day), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		l.file, l.curDay = f, day
		go l.prune(day)
	}
	_, _ = l.file.Write(b)
}

// prune 删除超过保留天数的文件（每天最多触发一次，代价可忽略）。
func (l *ReqLog) prune(today string) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	cutoff, err := time.Parse("2006-01-02", today)
	if err != nil {
		return
	}
	cutoff = cutoff.AddDate(0, 0, -l.keepDay)
	for _, e := range entries {
		name := e.Name()
		if len(name) < len("agg-requests-.jsonl") || name[:13] != "agg-requests-" {
			continue
		}
		day := name[13 : len(name)-len(".jsonl")]
		if t, err := time.Parse("2006-01-02", day); err == nil && t.Before(cutoff) {
			_ = os.Remove(filepath.Join(l.dir, name))
		}
	}
}

// Recent 读取最近 n 条事件（新的在前），跨天回溯最多 7 个文件。
func (l *ReqLog) Recent(n int) []ReqEvent {
	if l == nil || n <= 0 {
		return nil
	}
	files, err := filepath.Glob(filepath.Join(l.dir, "agg-requests-*.jsonl"))
	if err != nil {
		return nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))

	out := make([]ReqEvent, 0, n)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// 从末尾往前扫行（文件通常不大，直接按行切）
		lines := splitLines(raw)
		for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
			if len(lines[i]) == 0 {
				continue
			}
			var e ReqEvent
			if json.Unmarshal(lines[i], &e) == nil {
				out = append(out, e)
			}
		}
		if len(out) >= n {
			break
		}
	}
	return out
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
