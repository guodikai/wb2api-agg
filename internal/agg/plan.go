// plan.go 跨平台选择：给定"一个对外模型名的候选集"，决定这次请求交给哪个平台。
//
// 这是整个调度里唯一需要精确正确的地方，所以做成**纯函数**：不碰网络、不碰锁、
// 不读时间，输入输出都能在单测里逐条断言。
//
// 决策规则（用户口径）：
//  1. 冷却中的候选直接跳过（该平台刚失败过，别在同一个坑里再撞）。
//  2. 已知单位成本的排在未知之前；已知成本按升序（每百万 token 消耗越少越优先）。
//  3. 同成本按平台名字典序，保证结果可复现（同一个请求不会随机漂移）。
//
// 平台内部"选哪个账号"不在这里：那是账号池的职责（到期紧迫度、余额权重）。
package agg

import (
	"errors"
	"fmt"
	"strings"

	"github.com/misakano7545/workbuddy2api-panel/internal/router"
)

// ErrNoCandidate 没有任何可用候选：全部冷却、或该模型在已接入平台里都不存在。
var ErrNoCandidate = errors.New("agg: no usable candidate")

// Candidate 一个"平台上的某个模型"能否承接这次请求。
type Candidate struct {
	Channel   string  // 平台标识，与 channel.Kind 的字符串值一致
	Model     string  // 该平台上的真实模型 id（原样发给上游）
	UnitCost  float64 // 每百万 token 积分成本
	CostKnown bool    // 区分"0 成本"与"成本未知"
	Cooling   bool    // 该平台/通道处于冷却（限流、连续失败等）
	// Urgency 该平台的到期紧迫度倍率（1.0 起，口径同 misakano pick.go）。0 按 1。
	Urgency float64
}

// Decision 选择结果，带可解释原因（写进请求日志，便于事后追责）。
type Decision struct {
	Channel string
	Model   string
	// Reason 人类可读，例如 "cheapest known cost 0.30 (2 candidates, 1 cooling skipped)"
	Reason string
}

// Choose 在候选集里挑一个。candidates 可以跨平台、也可以只有一条。
func Choose(modelKey string, candidates []Candidate) (Decision, error) {
	if len(candidates) == 0 {
		return Decision{}, fmt.Errorf("%w: 模型 %q 没有候选平台", ErrNoCandidate, modelKey)
	}

	skipped := 0
	live := make([]router.Entry, 0, len(candidates))
	for _, c := range candidates {
		if c.Cooling {
			skipped++
			continue
		}
		live = append(live, router.Entry{
			Channel:   c.Channel,
			Model:     c.Model,
			UnitCost:  c.UnitCost,
			CostKnown: c.CostKnown,
			Urgency:   c.Urgency,
		})
	}
	if len(live) == 0 {
		return Decision{}, fmt.Errorf("%w: 模型 %q 的 %d 个候选平台全部冷却", ErrNoCandidate, modelKey, len(candidates))
	}

	ordered := router.Order(live) // 复用同一套成本/稳定排序规则，避免两处口径漂移
	best := ordered[0]

	var reason strings.Builder
	fmt.Fprintf(&reason, "%s", modelKey)
	if best.CostKnown {
		if best.Urgency > 1 {
			fmt.Fprintf(&reason, " → %s (cost %.2f/1M ÷ urgency %.2f = %.3f)", best.Channel, best.UnitCost, best.Urgency, best.UnitCost/best.Urgency)
		} else {
			fmt.Fprintf(&reason, " → %s (cost %.2f/1M)", best.Channel, best.UnitCost)
		}
	} else {
		fmt.Fprintf(&reason, " → %s (cost unknown)", best.Channel)
	}
	fmt.Fprintf(&reason, ", %d candidate(s)", len(live))
	if skipped > 0 {
		fmt.Fprintf(&reason, ", %d cooling skipped", skipped)
	}

	return Decision{Channel: best.Channel, Model: best.Model, Reason: reason.String()}, nil
}
