// wire.go 启动期装配：把已登录的平台适配器注册进 registry。
//
// 放在我们自己的包里，main.go 只多一行调用——这是与上游唯一的接触面，
// 上游重构 handler 不会影响我们（计划书 §七"上游冲突面≤2 行"）。
package agg

import (
	"log"
	"os"
	"path/filepath"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel"
	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

// RegistryFromEnv 从环境变量 WB_AGG_CREDS 指定的目录装配 registry。
//
//   - 目录不存在 / 没有码道账号 → 只返回空 registry（网关行为与上游完全一致）
//   - 有账号 → 注册码道适配器，`codearts:` 前缀的请求才可用
//
// 任何失败都只记日志、不阻断启动：网关的主线是 WorkBuddy，不能因为扩展平台
// 配置有问题就起不来。
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
	ids, err := store.List(creds.PlatformCodeArts)
	if err != nil || len(ids) == 0 {
		return reg
	}

	client := codearts.NewClient()
	src := creds.NewCodeArtsSource(store, client, codearts.DefaultLoginConfig())
	if err := reg.Register(codearts.NewAdapter(client, src, 0)); err != nil {
		log.Printf("[agg] 注册码道平台失败：%v", err)
		return reg
	}
	log.Printf("[agg] 已启用平台: %v（凭据目录 %s，码道账号 %d 个）",
		kindsOf(reg), filepath.Clean(dir), len(ids))
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
