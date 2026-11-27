// models_merge.go /v1/models 合并扩展平台模型。
//
// 上游只列 WorkBuddy 的模型；码道等平台的名字客户端根本看不到，只能手写。
// 这里在响应里追加（带通道前缀，如 codearts:glm-5.3-flash），并做三件事：
//   - 只合并 200 响应（401/500 原样放行，鉴权仍由上游负责）
//   - 去重（同 id 只留一份，前缀优先保证可路由）
//   - 缓存各平台目录 10 分钟，避免每次拉列表都打上游
package agg

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

type modelCacheEntry struct {
	at     time.Time
	models []channel.ModelInfo
}

var (
	modelCacheMu sync.Mutex
	modelCache   = map[channel.Kind]modelCacheEntry{}
)

const modelCacheTTL = 10 * time.Minute

func cachedModels(ctx context.Context, reg *channel.Registry, k channel.Kind) []channel.ModelInfo {
	modelCacheMu.Lock()
	if e, ok := modelCache[k]; ok && time.Since(e.at) < modelCacheTTL {
		modelCacheMu.Unlock()
		return e.models
	}
	modelCacheMu.Unlock()

	ad, err := reg.Get(k)
	if err != nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	list, err := ad.Models(cctx)
	if err != nil {
		// 失败不缓存，下次再试
		return nil
	}
	modelCacheMu.Lock()
	modelCache[k] = modelCacheEntry{at: time.Now(), models: list}
	modelCacheMu.Unlock()
	return list
}

// serveModelsWithPlatforms 捕获上游 /v1/models → 追加扩展平台模型 → 透传。
func serveModelsWithPlatforms(w http.ResponseWriter, next http.Handler, r *http.Request, reg *channel.Registry) {
	cw := &captureWriter{header: make(http.Header), code: http.StatusOK}
	next.ServeHTTP(cw, r)
	body := cw.body.Bytes()

	if cw.code == http.StatusOK && reg != nil {
		if merged, ok := mergeModels(body, reg, r.Context()); ok {
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

func mergeModels(body []byte, reg *channel.Registry, ctx context.Context) ([]byte, bool) {
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body, false
	}
	list, ok := doc["data"].([]any)
	if !ok {
		return body, false
	}

	seen := make(map[string]bool, len(list))
	for _, it := range list {
		if m, ok := it.(map[string]any); ok {
			if id, _ := m["id"].(string); id != "" {
				seen[id] = true
			}
		}
	}

	added := 0
	for _, k := range reg.Kinds() {
		if k == channel.KindWorkBuddy {
			continue // 上游已经覆盖默认通道
		}
		for _, m := range cachedModels(ctx, reg, k) {
			id := Qualify(k, m.ID)
			if seen[id] {
				continue
			}
			seen[id] = true
			item := map[string]any{
				"id":       id,
				"object":   "model",
				"owned_by": string(k),
			}
			list = append(list, item)
			added++
		}
	}
	if added == 0 {
		return body, false
	}
	doc["data"] = list
	out, err := json.Marshal(doc)
	if err != nil {
		return body, false
	}
	return out, true
}
