// signin_status.go 读签到脚本写出的状态文件，供任务中心使用。
package agg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SigninResult 一个脚本最近一次运行的结果。
type SigninResult struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	ExitCode   int    `json:"exit_code"`
	FinishedAt string `json:"finished_at"`
	Summary    string `json:"summary"`
	Missing    bool   `json:"missing,omitempty"`
}

func signinDir() string {
	if d := os.Getenv("WB_AGG_SIGNIN_DIR"); d != "" {
		return d
	}
	return "/opt/wb-agg-daily/status"
}

// loadSignin 读某个脚本的状态；不存在时 Missing=true。
func loadSignin(name string) SigninResult {
	p := filepath.Join(signinDir(), name+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		return SigninResult{Name: name, Missing: true, Summary: "还没有运行记录"}
	}
	var r SigninResult
	if json.Unmarshal(raw, &r) != nil {
		return SigninResult{Name: name, Missing: true, Summary: "状态文件损坏"}
	}
	r.Name = name
	// 失败就推一条（去重由 notifier 负责）；成功不打扰
	if !r.OK && !r.Missing {
		NotifySignin(name, r.OK, r.Summary)
	}
	return r
}

// signinAsTask 把脚本结果变成任务中心的一条任务项（不伪造进度）。
func signinAsTask(title string, r SigninResult) map[string]any {
	status := "failed"
	desc := r.Summary
	if r.Missing {
		status = "external"
		desc = "脚本还没有运行记录"
	} else if r.OK {
		status = "claimed"
	}
	when := r.FinishedAt
	if t, err := time.Parse("2006-01-02T15:04:05-0700", when); err == nil {
		when = t.Format("01-02 15:04")
	}
	if when != "" {
		desc = "[" + when + "] " + desc
	}
	claimed := !r.Missing && r.OK
	current := 0
	if claimed {
		current = 1
	}
	return map[string]any{
		"title":          title,
		"task_code":      "l0ne_" + r.Name,
		"task_desc":      desc,
		"accept_status":  status,
		"claimed":        claimed,
		"credit":         0,
		"current":        current,
		"target":         1,
		"progress_known": true,
	}
}

// wbSignedUIDs 读 WorkBuddy 签到脚本覆盖的账号（cn-xxxxxxxx 的 8 位前缀）。
// 只给这些账号挂任务，别把别的账号也标成"已签到"。
func wbSignedUIDs() map[string]bool {
	out := map[string]bool{}
	raw, err := os.ReadFile("/opt/wb-agg-daily/wbstate/wb_refresh_tokens.json")
	if err != nil {
		return out
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return out
	}
	for k := range m {
		if strings.HasPrefix(k, "cn-") {
			out[strings.TrimPrefix(k, "cn-")] = true
		}
	}
	return out
}


// todayCreditDelta 从积分趋势样本算今天的真实增量（首样本 → 末样本）。
// 返回 (delta, from, to, ok)。
func todayCreditDelta(platform string) (float64, float64, float64, bool) {
	h := SharedCreditHistory()
	if h == nil {
		return 0, 0, 0, false
	}
	samples := h.Recent(24, platform)
	if len(samples) == 0 {
		return 0, 0, 0, false
	}
	today := time.Now().Format("2006-01-02")
	var first, last float64
	found := false
	for _, s := range samples {
		if s.Time.Format("2006-01-02") != today {
			continue
		}
		if !found {
			first = s.Credits
			found = true
		}
		last = s.Credits
	}
	if !found {
		return 0, 0, 0, false
	}
	return last - first, first, last, true
}
