// Package router 把三家平台各不相同的模型 id 归一成可比较的键，做"自动同名匹配"：
// 同一个对外模型名，自动在 WorkBuddy / 码道 / 豆包之间互为候选。
//
// 归一规则（依据 2026-11-26 三家真实模型名核对的结论）：
//  1. 小写 + 去首尾空白。
//  2. 反复剥离已知平台/域前缀（可叠加）：cn: global: intl: workbuddy: codearts: doubao: …
//     WorkBuddy 的 `cn:DeepSeek-V4-flash` 与码道的 `deepseek-v4-flash` 归一后同名。
//  3. `_` 与空格统一成 `-`。
//  4. **不动版本号里的点**：`deepseek-v4.1-flash` 与 `deepseek-v4-flash` 是两个模型，
//     绝不能被模糊合并（这是最容易犯的"聪明错"）。
//  5. 平台自带的档位别名（fast-model / balanced-model / deep-model / auto）由平台内部
//     再调度到任意真实模型，跨平台没有对应物 → 标记为不可匹配，永不参与同名分组。
package router

import "strings"

// platformPrefixes 允许被剥离的前缀。只收录"确定是平台/域命名空间"的词，
// 避免把 `qwen:max` 这类模型自带前缀误剥。可以叠加（cn:codearts:xxx）。
var platformPrefixes = map[string]bool{
	"cn":        true,
	"global":    true,
	"intl":      true,
	"workbuddy": true,
	"wb":        true,
	"codearts":  true,
	"doubao":    true,
	"trae":      true,
	"qoder":     true,
	"minimax":   true,
	"gemini":    true,
}

// tierAliases 平台自带的档位别名：快速 / 均衡 / 专家 / auto。
// 它们的语义是"平台自己挑一个真实模型"，跨平台不可比，因此永不参与同名匹配。
var tierAliases = map[string]bool{
	"fast-model":     true,
	"balanced-model": true,
	"deep-model":     true,
	"auto":           true,
}

// Normalize 返回模型 id 的归一键，以及它是否允许参与跨平台同名匹配。
//
//	cn:DeepSeek-V4-flash  → "deepseek-v4-flash", true
//	deepseek-v4-flash     → "deepseek-v4-flash", true
//	cn:fast-model         → "fast-model",         false   // 档位别名，不匹配
//	deepseek-v4.1-flash   → "deepseek-v4.1-flash", true   // 版本点保留，不合并
func Normalize(id string) (key string, matchable bool) {
	s := strings.ToLower(strings.TrimSpace(id))
	for {
		i := strings.IndexByte(s, ':')
		if i <= 0 {
			break
		}
		if !platformPrefixes[s[:i]] {
			break
		}
		s = s[i+1:]
	}
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		return "", false
	}
	if tierAliases[s] {
		return s, false
	}
	return s, true
}

// Entry 一个平台侧的候选：某个对外名在该平台上的真实模型 id。
type Entry struct {
	Channel string  // 平台标识（channel.Kind 的字符串值）
	Model   string  // 发给该上游的原始 id，大小写敏感，原样使用
	UnitCost float64 // 每百万 token 的积分成本（平台内货币）
	CostKnown bool  // 区分"成本 0"与"成本未知"
	// Urgency 该平台当前"到期紧迫度"倍率（1.0 起，越逼近到期越大，口径同 misakano
	// pick.go 的 1.5~3.0）。0 或负数按 1 处理。
	//
	// 为什么做成无量纲倍率而不做汇率：三家积分不可通兑，换汇率是拍脑袋；而紧迫度
	// 只是"这批额度多久要作废"，与货币无关。排序用 UnitCost/Urgency（生效成本），
	// 于是"今天到期但稍贵"的通道会自然排到前面。
	Urgency float64
}

// effectiveCost 生效成本：单位成本 ÷ 紧迫度。紧迫度 <=0 视为 1（不折扣）。
func effectiveCost(e Entry) float64 {
	u := e.Urgency
	if u <= 0 {
		u = 1
	}
	return e.UnitCost / u
}

// Group 按归一键把候选分组。返回：
//   - groups: 归一键 → 候选列表（只含可匹配的键；单元素组也会出现，调用方按需处理）
//   - local: 不可匹配的条目（档位别名、非法 id），原样返回给调用方单独注册
//
// 组的顺序不重要；组内顺序由 Order 决定。
func Group(entries []Entry) (groups map[string][]Entry, local []Entry) {
	groups = make(map[string][]Entry)
	for _, e := range entries {
		key, ok := Normalize(e.Model)
		if !ok {
			local = append(local, e)
			continue
		}
		groups[key] = append(groups[key], e)
	}
	return groups, local
}

// Order 组内排序：单位成本升序（未知成本排最后），同成本按平台稳定顺序（按
// channel 名字典序）保证结果可复现。平台内"哪个账号"的到期优先不在这里做——
// 那是账号池的职责，本层只决定"先试哪个平台/模型"。
func Order(in []Entry) []Entry {
	out := make([]Entry, len(in))
	copy(out, in)
	// 插入排序：候选通常 < 10，避免引入 sort 依赖的额外分配。
	for i := 1; i < len(out); i++ {
		cur := out[i]
		j := i - 1
		for j >= 0 && less(cur, out[j]) {
			out[j+1] = out[j]
			j--
		}
		out[j+1] = cur
	}
	return out
}

func less(a, b Entry) bool {
	ak, bk := a.CostKnown, b.CostKnown
	if ak != bk {
		return ak // 已知成本优先于未知
	}
	if ak && bk {
		ca, cb := effectiveCost(a), effectiveCost(b)
		if ca != cb {
			return ca < cb
		}
	}
	return a.Channel < b.Channel
}
