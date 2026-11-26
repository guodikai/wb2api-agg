// credits_history.go 余额趋势：每次积分刷新写一条样本，面板画折线。
//
// 文件：/opt/wb2api-agg/data/agg-credits.jsonl（逐行 JSON，保留 30 天）
// 一条样本：{t, platform, account, credits, total, used, expiring}
package agg

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// CreditSample 一条余额样本。
type CreditSample struct {
	Time     time.Time `json:"t"`
	Platform string    `json:"platform"`
	Account  string    `json:"account"`
	Credits  float64   `json:"credits"`
	Total    float64   `json:"total"`
	Used     float64   `json:"used"`
	Expiring float64   `json:"expiring"`
}

// CreditHistory 样本写入与读取（并发安全）。
type CreditHistory struct {
	dir string
	mu  sync.Mutex
}

// NewCreditHistory 打开目录。
func NewCreditHistory(dir string) (*CreditHistory, error) {
	if dir == "" {
		dir = "/opt/wb2api-agg/data"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &CreditHistory{dir: dir}, nil
}

func (h *CreditHistory) file() string { return filepath.Join(h.dir, "agg-credits.jsonl") }

// Append 追加一条样本（失败静默，不影响主流程）。
func (h *CreditHistory) Append(s CreditSample) {
	if h == nil {
		return
	}
	if s.Time.IsZero() {
		s.Time = time.Now()
	}
	b, err := json.Marshal(s)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	f, err := os.OpenFile(h.file(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// Recent 取最近 hours 小时内的样本（按时间升序，便于直接画线）。
func (h *CreditHistory) Recent(hours int, platform string) []CreditSample {
	if h == nil {
		return nil
	}
	if hours <= 0 {
		hours = 72
	}
	cutoff := time.Now().Add(-time.Duration(hours) * time.Hour)

	f, err := os.Open(h.file())
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []CreditSample
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var s CreditSample
		if json.Unmarshal(sc.Bytes(), &s) != nil {
			continue
		}
		if s.Time.Before(cutoff) {
			continue
		}
		if platform != "" && s.Platform != platform {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

var (
	creditHistOnce sync.Once
	creditHistRef  *CreditHistory
)

// SharedCreditHistory 单例（目录同请求日志，WB_AGG_DATA）。
func SharedCreditHistory() *CreditHistory {
	creditHistOnce.Do(func() {
		dir := os.Getenv("WB_AGG_DATA")
		if dir == "" {
			dir = "/opt/wb2api-agg/data"
		}
		if h, err := NewCreditHistory(dir); err == nil {
			creditHistRef = h
		}
	})
	return creditHistRef
}
