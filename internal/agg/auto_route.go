// auto_route.go 裸模型名的跨平台自动路由。
//
// 规则（用户口径 B：全部自动）：
//   - codearts:x / doubao:x  → 强制指定平台（原逻辑）
//   - cn:x / global:x        → WorkBuddy 的 realm 前缀，仍归默认通道
//   - 裸名 deepseek-v4.1-flash → 自动在 WorkBuddy 与码道之间选：
//       1) 谁真的列了这个模型（不在目录里就不给它）
//       2) 都列了：优先"最早到期的额度"，没有到期信息时按剩余比例
//       3) 都没列：交回 WorkBuddy（它的网关对未知模型名原样转发）
//
// 数据全部来自本进程可直达的上游响应（缓存 60 秒），不额外打外部接口。
package agg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
	"github.com/misakano7545/workbuddy2api-panel/internal/router"
)

// platformStat 一个平台的额度概况，用于比较到期紧迫度。
type platformStat struct {
	kind     channel.Kind
	remain   float64
	total    float64
	earliest time.Time
}

type statsCache struct {
	at    time.Time
	byKnd map[channel.Kind]platformStat
	names map[string]map[channel.Kind]string
}

var (
	statsMu    sync.Mutex
	statsEntry *statsCache
)

const statsTTL = 60 * time.Second

func (m *Mux) loadStats(ctx context.Context, r *http.Request) *statsCache {
	statsMu.Lock()
	if statsEntry != nil && time.Since(statsEntry.at) < statsTTL {
		e := statsEntry
		statsMu.Unlock()
		return e
	}
	statsMu.Unlock()

	e := &statsCache{at: time.Now(), byKnd: map[channel.Kind]platformStat{}, names: map[string]map[channel.Kind]string{}}

	// 1) 上游 /v1/models（经 Next，避免递归）
	if body := m.captureNext(r, http.MethodGet, "/v1/models"); body != nil {
		var doc struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &doc) == nil {
			for _, it := range doc.Data {
				id := it.ID
				if strings.Contains(id, ":") && !strings.HasPrefix(id, "cn:") && !strings.HasPrefix(id, "global:") {
					continue
				}
				key, matchable := router.Normalize(id)
				if !matchable || key == "" {
					continue
				}
				if e.names[key] == nil {
					e.names[key] = map[channel.Kind]string{}
				}
				e.names[key][channel.KindWorkBuddy] = id
			}
		}
	}

	// 2) 扩展平台目录
	if m.Registry != nil {
		for _, k := range m.Registry.Kinds() {
			if k == channel.KindWorkBuddy {
				continue
			}
			for _, mi := range cachedModels(ctx, m.Registry, k) {
				key, matchable := router.Normalize(mi.ID)
				if !matchable || key == "" {
					continue
				}
				if e.names[key] == nil {
					e.names[key] = map[channel.Kind]string{}
				}
				e.names[key][k] = mi.ID
			}
		}
	}

	// 3) 额度概况：上游 overview（含我们合并进去的扩展平台账号）
	if body := m.captureNext(r, http.MethodGet, "/panel/api/overview"); body != nil {
		var doc struct {
			Accounts []struct {
				UID            string  `json:"uid"`
				Credits        float64 `json:"credits"`
				CreditsTotal   float64 `json:"credits_total"`
				EarliestExpiry string  `json:"credits_earliest_expiry"`
				Platform       string  `json:"platform"`
			} `json:"accounts"`
		}
		if json.Unmarshal(body, &doc) == nil {
			for _, a := range doc.Accounts {
				k := channel.KindWorkBuddy
				if strings.HasPrefix(a.UID, uidPrefixCodeArts) {
					k = channel.KindCodeArts
				} else if a.Platform != "" {
					k = channel.Kind(a.Platform)
				}
				st := e.byKnd[k]
				st.kind = k
				st.remain += a.Credits
				st.total += a.CreditsTotal
				if t, err := time.Parse(time.RFC3339, a.EarliestExpiry); err == nil && !t.IsZero() {
					if st.earliest.IsZero() || t.Before(st.earliest) {
						st.earliest = t
					}
				}
				e.byKnd[k] = st
			}
		}
	}

	statsMu.Lock()
	statsEntry = e
	statsMu.Unlock()
	return e
}

// captureNext 用 Next 处理器跑一次内部请求，返回 200 的响应体，否则 nil。
func (m *Mux) captureNext(r *http.Request, method, path string) []byte {
	req := httptest.NewRequest(method, path, nil)
	for _, h := range []string{"Authorization", "X-Api-Key"} {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}
	cw := &captureWriter{header: make(http.Header), code: http.StatusOK}
	m.Next.ServeHTTP(cw, req)
	if cw.code != http.StatusOK {
		return nil
	}
	return cw.body.Bytes()
}

// autoPick 决定裸模型名走哪个平台；kind 为空表示交回 WorkBuddy。
func (m *Mux) autoPick(ctx context.Context, r *http.Request, model string) (channel.Kind, string, bool) {
	key, matchable := router.Normalize(model)
	if !matchable || key == "" {
		return "", "", false
	}
	st := m.loadStats(ctx, r)
	owners := st.names[key]
	if len(owners) == 0 {
		return "", "", false
	}

	type cand struct {
		kind  channel.Kind
		model string
		score float64
	}
	var cands []cand
	for k, id := range owners {
		s := st.byKnd[k]
		c := cand{kind: k, model: id}
		switch {
		case !s.earliest.IsZero():
			days := time.Until(s.earliest).Hours() / 24
			if days < 0 {
				days = 0
			}
			c.score = 100 - days
		case s.total > 0:
			c.score = 50 + 50*(s.remain/s.total)
		default:
			c.score = 10
		}
		if s.total > 0 && s.remain <= 0 {
			c.score = -1
		}
		cands = append(cands, c)
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })

	for _, c := range cands {
		if c.score < 0 {
			continue
		}
		if c.kind == channel.KindWorkBuddy {
			return "", "", false
		}
		return c.kind, c.model, true
	}
	return "", "", false
}
