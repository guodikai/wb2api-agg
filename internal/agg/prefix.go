// prefix.go 模型名里的"通道前缀"解析。
//
// 关键区分（踩过就会痛）：`cn:` / `global:` 是 **WorkBuddy 自己的 realm 前缀**，
// 属于模型 id 的一部分，必须原样传给上游；而 `codearts:` / `doubao:` 是我们网关
// 用来选通道的前缀，剥掉之后剩下的才是上游模型 id。
//
// 所以这里只认三个"通道前缀"，把 cn:/global: 留给 WorkBuddy 通道自己处理。
package agg

import (
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
)

// channelPrefixes 网关层的通道前缀（小写）。顺序无关，最长匹配由调用方保证不含歧义。
var channelPrefixes = map[string]channel.Kind{
	"codearts":  channel.KindCodeArts,
	"doubao":    channel.KindDoubao,
	"workbuddy": channel.KindWorkBuddy,
}

// Target 解析结果。
type Target struct {
	// Kind 目标通道。没有通道前缀时为零值 ""，表示"交给默认通道（WorkBuddy）"。
	Kind channel.Kind
	// Explicit 是否由客户端显式指定了通道前缀。
	Explicit bool
	// Model 要发给上游的模型 id（通道前缀已剥，cn:/global: 等 realm 前缀保留）。
	Model string
}

// SplitModel 解析 "codearts:deepseek-v3" 这类带通道前缀的模型名。
//
//	codearts:deepseek-v3   → {Kind: codearts, Explicit: true,  Model: "deepseek-v3"}
//	doubao:DeepSeek Flash  → {Kind: doubao,   Explicit: true,  Model: "DeepSeek Flash"}
//	cn:deepseek-v4-flash   → {Kind: "",       Explicit: false, Model: "cn:deepseek-v4-flash"}
//	hy4flash-free          → {Kind: "",       Explicit: false, Model: "hy4flash-free"}
func SplitModel(model string) Target {
	s := strings.TrimSpace(model)
	i := strings.IndexByte(s, ':')
	if i <= 0 {
		return Target{Model: s}
	}
	if k, ok := channelPrefixes[strings.ToLower(s[:i])]; ok {
		return Target{Kind: k, Explicit: true, Model: strings.TrimSpace(s[i+1:])}
	}
	// 不是通道前缀（例如 WorkBuddy 的 cn:），原样交给默认通道。
	return Target{Model: s}
}

// Qualify 反向操作：给非默认通道的模型名补上通道前缀，用于 /v1/models 输出。
// WorkBuddy（默认通道）保持裸 id——否则现有客户端的模型名会全部失配。
func Qualify(k channel.Kind, model string) string {
	if k == channel.KindWorkBuddy || k == "" {
		return model
	}
	return string(k) + ":" + model
}
