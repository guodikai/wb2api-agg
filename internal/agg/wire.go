// wire.go 启动期装配：把已登录的平台适配器注册进 registry。
//
// 放在我们自己的包里，main.go 只多一行调用——这是与上游唯一的接触面。
package agg

import (
	"log"
	"os"
	"path/filepath"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
	"github.com/misakano7545/workbuddy2api-panel/internal/channel/doubao"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

// RegistryFromEnv 从 WB_AGG_CREDS（默认 /opt/wb2api-agg/creds）装配 registry。
//
//   - 目录不存在 → 空 registry（行为与上游完全一致）
//   - 有码道账号 → 注册码道
//   - 有豆包账号 → 注册豆包
//
// 任何失败只记日志、不阻断启动：网关主线是 WorkBuddy，不能因为扩展平台配置有问题就起不来。
func RegistryFromEnv() *channel.Registry {
	reg := channel.NewRegistry()

	dir := os.Getenv("WB_AGG_CREDS")
	if dir == "" {
		dir = "/opt/wb2api-agg/creds"
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return reg
	}
	store, err := creds.New(dir)
	if err != nil {
		log.Printf("[agg] 凭据目录不可用（%s）：%v；仅启用上游平台", dir, err)
		return reg
	}

	// 码道：消费侧独占 RT，自己刷新。
	if ids, _ := store.List(creds.PlatformCodeArts); len(ids) > 0 {
		client := codearts.New(0)
		src := creds.NewCodeArtsSource(store, client, codearts.DefaultLoginConfig())
		if err := reg.Register(codearts.NewAdapter(client, src)); err != nil {
			log.Printf("[agg] 注册码道平台失败：%v", err)
		} else {
			log.Printf("[agg] 码道已启用（%d 个账号）", len(ids))
		}
	}

	// 豆包：cookie 由签到引擎维护，这里只读。
	if ids, _ := store.List(creds.PlatformDoubao); len(ids) > 0 {
		if err := reg.Register(doubao.NewAdapter(doubao.New(""), creds.NewDoubaoSource(store))); err != nil {
			log.Printf("[agg] 注册豆包平台失败：%v", err)
		} else {
			log.Printf("[agg] 豆包已启用（%d 个账号）", len(ids))
		}
	}

	if len(reg.Kinds()) > 0 {
		log.Printf("[agg] 已启用平台: %v（凭据目录 %s）", kindsOf(reg), filepath.Clean(dir))
	}
	return reg
}

func kindsOf(reg *channel.Registry) []string {
	ks := reg.Kinds()
	out := make([]string, 0, len(ks))
	for _, k := range ks {
		out = append(out, string(k))
	}
	return out
}
