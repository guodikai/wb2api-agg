// models.go 码道模型目录（/v1/model/builtin，Agent-Type: PromptCenter）。
//
// 移植来源：HITZY2002/codearts2api internal/upstream/models.go（MIT）。
//
// 与 WorkBuddy 的差别：码道不提供"每百万 token 积分单价"，只有积分余额与用量。
// 所以 Codearts 的模型在调度里一律 CostKnown=false（排在同成本之后），
// 直到我们从用量差反推出经验单价——这一点在计划书 §三之二 里写明，不猜。
package codearts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ModelInfo 码道侧模型条目。
type ModelInfo struct {
	ID            string
	Name          string
	Desc          string
	ContextWindow int64
	MaxTokens     int64
}

// FetchModels 拉内置模型目录（需要签名 + security token）。
func (c *Client) FetchModels(ctx context.Context, cred SignCredential, endpoint string) ([]ModelInfo, error) {
	if endpoint == "" {
		endpoint = c.snapHost + EpModelBuiltin
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HdrAgentType, AgentType)
	req.Header.Set(HdrLanguage, LangZHCN)
	if cred.SecurityToken != "" {
		req.Header.Set("X-Security-Token", cred.SecurityToken)
	}
	// 空 body 也要签名（payload hash 为空串的 sha256）。
	SignRequest(req, []byte{}, cred)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return nil, &UpstreamError{Status: resp.StatusCode, Message: truncate(string(raw), 300), Path: EpModelBuiltin}
	}

	var out struct {
		BuiltinModels []struct {
			ModelID       string `json:"model_id"`
			ModelName     string `json:"model_name"`
			ModelDesc     string `json:"model_desc"`
			ContextWindow int64  `json:"context_window"`
			MaxTokens     int64  `json:"max_tokens"`
		} `json:"builtinModels"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析模型目录失败: %w", err)
	}
	infos := make([]ModelInfo, 0, len(out.BuiltinModels))
	for _, m := range out.BuiltinModels {
		id := m.ModelID
		if id == "" {
			id = m.ModelName
		}
		if id == "" {
			continue
		}
		infos = append(infos, ModelInfo{
			ID: id, Name: id, Desc: m.ModelDesc,
			ContextWindow: m.ContextWindow, MaxTokens: m.MaxTokens,
		})
	}
	return infos, nil
}

// CanonicalModel 规范化模型名：去掉首尾空白并小写（上游对大小写敏感，
// 但大小写变体在目录里是同一个模型；调用方仍应优先用目录里的原始拼写）。
func CanonicalModel(model string) string { return strings.ToLower(strings.TrimSpace(model)) }
