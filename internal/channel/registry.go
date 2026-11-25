// registry.go 平台适配器注册表。
//
// 设计意图：新增一个平台 = 新目录 + 在 main 侧注册一次，调度层与面板都不需要改。
// 注册表只做"Kind → Adapter"的映射与并发安全读写，不掺任何平台逻辑。
package channel

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ErrNotRegistered 该平台没有适配器（未接入，或尚未在启动时注册）。
// 与 ErrNotSupported 区分：ErrNotRegistered = 平台根本没接；ErrNotSupported = 平台接了但
// 不支持这个操作（例如该平台没有图像能力）。前者应报 400 并说明未接入，不可轮转。
var ErrNotRegistered = errors.New("channel: adapter not registered")

// Registry 并发安全的适配器注册表。
type Registry struct {
	mu       sync.RWMutex
	adapters map[Kind]Adapter
}

// NewRegistry 空注册表。
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[Kind]Adapter, 3)}
}

// Register 注册一个适配器。nil、非法 Kind、重复注册都返回错误——
// 重复注册是编程错误，必须在启动时暴露，不能静默覆盖。
func (r *Registry) Register(a Adapter) error {
	if a == nil {
		return errors.New("channel: 注册了 nil 适配器")
	}
	k := a.Kind()
	if !k.Valid() {
		return fmt.Errorf("channel: 未知平台 %q", string(k))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.adapters[k]; dup {
		return fmt.Errorf("channel: 平台 %q 重复注册", string(k))
	}
	r.adapters[k] = a
	return nil
}

// Get 取适配器。
func (r *Registry) Get(k Kind) (Adapter, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a, ok := r.adapters[k]; ok {
		return a, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotRegistered, string(k))
}

// Has 报告该平台是否已接入（面板据此决定是否渲染页签）。
func (r *Registry) Has(k Kind) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.adapters[k]
	return ok
}

// Kinds 已注册的平台，按字符串排序保证输出稳定（面板/日志可回读断言）。
func (r *Registry) Kinds() []Kind {
	r.mu.RLock()
	out := make([]Kind, 0, len(r.adapters))
	for k := range r.adapters {
		out = append(out, k)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
