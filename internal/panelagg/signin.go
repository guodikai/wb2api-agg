// pa_signin.go 面板：签到脚本的运行状态。
//
// 两个 L0NE 脚本（WorkBuddy-Daily / CodeArts-Daily）在 /opt/wb-agg-daily 下由
// systemd timer 跑；每次跑完把结果写进 status/<name>.json。这里只读这些文件，
// 不去反向调用脚本——面板刷新不能触发签到。
package panelagg

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type signinStatus struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	ExitCode   int    `json:"exit_code"`
	FinishedAt string `json:"finished_at"`
	Summary    string `json:"summary"`
	// 下面两个由本接口补充
	LogTail string `json:"log_tail,omitempty"`
	Missing bool   `json:"missing,omitempty"`
}

// signinStatus GET /panel/agg/api/signin
func (h *Handler) signinStatus(w http.ResponseWriter, r *http.Request) {
	dir := h.cfg.SigninDir
	if dir == "" {
		dir = "/opt/wb-agg-daily/status"
	}
	names := []string{"wb", "codearts"}
	out := make([]signinStatus, 0, len(names))
	for _, n := range names {
		var st signinStatus
		p := filepath.Join(dir, n+".json")
		raw, err := os.ReadFile(p)
		if err != nil {
			st = signinStatus{Name: n, Missing: true, Summary: "还没有运行记录"}
		} else if json.Unmarshal(raw, &st) != nil {
			st = signinStatus{Name: n, Missing: true, Summary: "状态文件损坏"}
		} else {
			st.Name = n
		}
		if tail := readTail(filepath.Join(h.cfg.SigninLogDir, n+".log"), 6); tail != "" {
			st.LogTail = tail
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func readTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	size := st.Size()
	startAt := size - 6000
	if startAt < 0 {
		startAt = 0
	}
	buf := make([]byte, size-startAt)
	if _, err := f.ReadAt(buf, startAt); err != nil {
		return ""
	}
	all := strings.Split(string(buf), "\n")
	kept := all
	if len(all) > n {
		kept = all[len(all)-n:]
	}
	return strings.TrimRight(strings.Join(kept, "\n"), "\n")
}
