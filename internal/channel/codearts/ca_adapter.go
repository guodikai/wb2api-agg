// adapter.go 把码道协议层包成网关的 channel.Adapter。
//
// 这一层只做三件事：取凭据、调协议、把 SSE 帧按标准形状写回调用方。
// 平台内"选哪个账号"由调用方（agg/池）决定，这里只认传进来的 accountID。
package codearts

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

// CredentialSource 凭据来源（由上层凭据层实现：共享目录 + flock + exp 比较）。
type CredentialSource interface {
	// Any 返回任意一个可用账号的凭据，用于目录探测。
	Any(ctx context.Context) (accountID string, cred SignCredential, err error)
	// For 返回指定账号的凭据，用于 chat。
	For(ctx context.Context, accountID string) (SignCredential, error)
}

// Adapter 码道通道适配器。
type Adapter struct {
	client *Client
	creds  CredentialSource
	ttl    time.Duration

	mu     sync.Mutex
	cached []channel.ModelInfo
	at     time.Time
}

// NewAdapter 构造适配器。ttl<=0 时目录缓存 10 分钟。
func NewAdapter(c *Client, src CredentialSource, ttl time.Duration) *Adapter {
	if c == nil {
		c = NewClient()
	}
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &Adapter{client: c, creds: src, ttl: ttl}
}

// Kind 实现 channel.Adapter。
func (a *Adapter) Kind() channel.Kind { return channel.KindCodeArts }

// Models 实现 channel.Adapter：拉目录并转成网关的 ModelInfo。
// 码道没有"每百万 token 单价"，因此 CostKnown=false（调度里排在同成本之后）。
func (a *Adapter) Models(ctx context.Context) ([]channel.ModelInfo, error) {
	a.mu.Lock()
	if len(a.cached) > 0 && time.Since(a.at) < a.ttl {
		out := append([]channel.ModelInfo(nil), a.cached...)
		a.mu.Unlock()
		return out, nil
	}
	a.mu.Unlock()

	_, cred, err := a.creds.Any(ctx)
	if err != nil {
		return nil, err
	}
	infos, err := a.client.FetchModels(ctx, cred, "")
	if err != nil {
		return nil, err
	}
	out := make([]channel.ModelInfo, 0, len(infos))
	for _, m := range infos {
		out = append(out, channel.ModelInfo{ID: m.ID, CostKnown: false})
	}

	a.mu.Lock()
	a.cached, a.at = out, time.Now()
	a.mu.Unlock()
	return append([]channel.ModelInfo(nil), out...), nil
}

// Chat 实现 channel.Adapter：把上游 SSE 重写成标准帧写入 w。
//
// 为什么不直接 io.Copy：上游用注释行做心跳、可能用多行 data 拼事件，直接透传
// 会让下游解析器各自踩一遍坑。这里统一成 "data: <json>\n\n" + 结尾 [DONE]。
func (a *Adapter) Chat(ctx context.Context, accountID, model string, body []byte, w io.Writer) error {
	// accountID 为空 = 没指定账号，由本平台内部选（当前策略：第一个可用账号）。
	// 平台内的"到期优先/余额权重"在 P4 接入统一调度后由上层传入具体账号。
	var cred SignCredential
	var err error
	if strings.TrimSpace(accountID) == "" {
		_, cred, err = a.creds.Any(ctx)
	} else {
		cred, err = a.creds.For(ctx, accountID)
	}
	if err != nil {
		return err
	}
	rc, err := a.client.ChatStream(ctx, model, body, cred, ChatOptions{})
	if err != nil {
		return err
	}
	defer rc.Close()

	err = ReadSSE(rc, func(data []byte) error {
		frame := make([]byte, 0, len(data)+8)
		frame = append(frame, "data: "...)
		frame = append(frame, data...)
		frame = append(frame, '\n', '\n')
		_, werr := w.Write(frame)
		return werr
	})
	// ReadSSE 在遇到上游 [DONE] 时返回 io.EOF——那个标记已被我们消费掉，
	// 所以无论哪条路径结束，都要给下游补一个 [DONE]，保证帧序列完整。
	if err == io.EOF {
		err = nil
	}
	if err != nil {
		return err
	}
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	return nil
}
