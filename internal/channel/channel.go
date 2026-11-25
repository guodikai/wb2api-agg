// Package channel 定义「一个上游平台」的适配器契约。
//
// 背景：这个网关要同时对接 WorkBuddy / 码道(CodeArts) / 豆包工作，并且由**同一个账号池**
// 统一调度（到期优先 + 单位成本）。所以每个平台都必须收敛到同一组最小语义：
//   - 列出它能提供的模型（含单位成本，供跨平台排序）
//   - 用某个账号发一次 chat（流式写 w，由调用方决定是 SSE 直通还是聚合）
//
// 本包只放契约与共享值对象，不放任何平台的实现——实现分别在
// internal/channel/workbuddy、internal/channel/codearts、internal/channel/doubao，
// 全部是新增目录，不修改上游文件，便于跟随上游同步。
package channel

import (
	"context"
	"errors"
	"io"
	"time"
)

// Kind 平台标识。字符串值同时用作配置键、面板分组键与 Redis 命名空间的一部分，
// 改动等于破坏兼容，勿改。
type Kind string

const (
	KindWorkBuddy Kind = "workbuddy"
	KindCodeArts  Kind = "codearts"
	KindDoubao    Kind = "doubao"
)

// Valid 报告 k 是否是已知平台。
func (k Kind) Valid() bool {
	switch k {
	case KindWorkBuddy, KindCodeArts, KindDoubao:
		return true
	}
	return false
}

// ErrNotSupported 适配器明确不支持该操作（例如该平台没有对应模型/能力）。
// 与「调用失败」区分：不支持是确定性的，调度器应直接换下一个候选，不做冷却。
var ErrNotSupported = errors.New("channel: operation not supported")

// ModelInfo 一个平台侧的模型条目。
type ModelInfo struct {
	// ID 发给该上游时使用的原始模型 id（大小写敏感，不得规范化）。
	ID string
	// UnitCost 该模型每百万 token 的积分成本（平台内货币）。0 表示未知，
	// 排序时 UnknownCost 会把它排在同成本之后但在"确实更贵"之前。
	UnitCost float64
	// CostKnown 区分「成本为 0」与「成本未知」。
	CostKnown bool
}

// Account 参与统一调度的账号最小视图。
//
// 完整凭据（cookie/token/设备指纹）留在各适配器自己的存储里；调度器只需要这几个
// 字段就能算权重，避免把三家的凭据结构糊在一起。
type Account struct {
	ID     string    // 平台内唯一 id（账号文件名或 uid）
	Kind   Kind      // 所属平台
	Credits float64  // 当前余额（平台内货币）
	// EarliestExpiry 最早到期的那批额度的到期时间；零值 = 无到期概念（如豆包额度）。
	EarliestExpiry time.Time
	// CoolingUntil 非零表示该账号在冷却中，调度器应跳过。
	CoolingUntil time.Time
}

// Adapter 一个上游平台的适配器。
type Adapter interface {
	// Kind 平台标识。
	Kind() Kind
	// Models 列出该平台当前可用的模型。允许网络调用，调用方负责 ctx 超时。
	Models(ctx context.Context) ([]ModelInfo, error)
	// Chat 用指定账号发一次 chat。请求/响应均为 OpenAI 兼容形状（body 已是 JSON）。
	// 返回的字节流原样写进 w（SSE 直通，或整段 JSON），由调用方决定是否再加工。
	Chat(ctx context.Context, accountID, model string, body []byte, w io.Writer) error
}
