// usage.go 跨平台用量上报：适配器解析到上游 usage 时，通过 context 把数字交给中间件。
//
// 为什么不改 Adapter.Chat 的签名：那会牵动所有平台实现与已有测试。用 context 传一个
// 可选的收集器，适配器看到就填，看不到什么都不用做。
package channel

import "context"

// Usage 一次请求的实际用量。
type Usage struct {
	Prompt     int64  `json:"prompt_tokens"`
	Completion int64  `json:"completion_tokens"`
	Effort     string `json:"effort,omitempty"`
	Model      string `json:"model,omitempty"`   // 上游实际使用的模型（可能与请求不同）
	Account    string `json:"account,omitempty"` // 实际服务本次请求的账号（平台内 id / 用户名）
}

type usageKey struct{}

// WithUsage 把收集器放进 context。
func WithUsage(ctx context.Context, u *Usage) context.Context {
	if u == nil {
		return ctx
	}
	return context.WithValue(ctx, usageKey{}, u)
}

// ReportUsage 适配器调用：把解析到的用量写进收集器（覆盖式，取最后一次）。
func ReportUsage(ctx context.Context, u Usage) {
	if sink, ok := ctx.Value(usageKey{}).(*Usage); ok && sink != nil {
		if u.Prompt > 0 {
			sink.Prompt = u.Prompt
		}
		if u.Completion > 0 {
			sink.Completion = u.Completion
		}
		if u.Effort != "" {
			sink.Effort = u.Effort
		}
		if u.Model != "" {
			sink.Model = u.Model
		}
		if u.Account != "" {
			sink.Account = u.Account
		}
	}
}
