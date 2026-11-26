// adapter.go 豆包通道的薄转接层（协议在 api.go）。
package doubao

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

// CredentialSource 账号来源（由 creds 包实现；豆包 cookie 的写者是签到引擎，这里只读）。
type CredentialSource interface {
	Any(ctx context.Context) (*Account, error)
	For(ctx context.Context, id string) (*Account, error)
}

// Adapter 豆包通道适配器。
type Adapter struct {
	client *Client
	creds  CredentialSource
	// path 对话端点：默认 /chat/completion；工作/Agent 形态用 /samantha/chat/completion。
	path string
}

// NewAdapter 构造。
func NewAdapter(c *Client, src CredentialSource) *Adapter {
	if c == nil {
		c = New("")
	}
	return &Adapter{client: c, creds: src, path: PathChat}
}

// Kind 实现 channel.Adapter。
func (a *Adapter) Kind() channel.Kind { return channel.KindDoubao }

// Models 豆包没有 OpenAI 风格的模型目录接口；这里给出档位名。
// 真实上游 model_item_key 由请求时透传；CostKnown 恒 false（豆包按订阅额度，不按 token 计价）。
func (a *Adapter) Models(ctx context.Context) ([]channel.ModelInfo, error) {
	return []channel.ModelInfo{
		{ID: "doubao-fast", CostKnown: false},
		{ID: "doubao-balanced", CostKnown: false},
		{ID: "doubao-expert", CostKnown: false},
		{ID: "doubao-auto", CostKnown: false},
	}, nil
}

// Chat 把 OpenAI 请求转成豆包请求并透传 SSE。
//
// 第一版只做"文本 + 流式"：model 换成目标 id、强制 stream=true，其余字段原样带上；
// 工具桥接与图片在后续阶段做（与码道保持同一节奏）。
func (a *Adapter) Chat(ctx context.Context, accountID, model string, body []byte, w io.Writer) error {
	var acct *Account
	var err error
	if strings.TrimSpace(accountID) == "" {
		acct, err = a.creds.Any(ctx)
	} else {
		acct, err = a.creds.For(ctx, accountID)
	}
	if err != nil {
		return err
	}

	payload := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return err
		}
	}
	payload["model"] = model
	payload["stream"] = true // 内部一律流式；非流式由 agg.Mux 聚合

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	resp, err := a.client.do(ctx, "POST", a.path, acct.query(), strings.NewReader(string(raw)), acct, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}
