// panel_wire.go 把我们的面板接到中间件上。
//
// api_key 从配置文件里读（不单独在 systemd 里重复放密钥）：
//   WB_AGG_CONFIG 指定配置文件路径，默认 /opt/wb2api-agg/config.json。
// 读不到就不启用面板（返回 nil），避免因为扩展功能把网关搞挂。
package agg

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/misakano7545/workbuddy2api-panel/internal/channel/codearts"
	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
	"github.com/misakano7545/workbuddy2api-panel/internal/panelagg"
)

func buildPanel() http.Handler {
	cfgPath := os.Getenv("WB_AGG_CONFIG")
	if cfgPath == "" {
		cfgPath = "/opt/wb2api-agg/config.json"
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil // 配置文件都读不到，说明不是我们的部署形态，安静退出
	}
	var cfg struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		log.Printf("[agg] 解析配置失败，面板未启用: %v", err)
		return nil
	}

	credsDir := os.Getenv("WB_AGG_CREDS")
	if credsDir == "" {
		credsDir = "/opt/wb2api-agg/creds"
	}
	store, err := creds.New(credsDir)
	if err != nil {
		log.Printf("[agg] 凭据目录不可用，面板未启用: %v", err)
		return nil
	}

	return panelagg.New(panelagg.Config{
		APIKey: cfg.APIKey,
		Store:  store,
		Client: codearts.New(0),
		Login:  codearts.DefaultLoginConfig(),
	})
}
