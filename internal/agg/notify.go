// notify.go 轻量告警：签到失败 / 凭据失效时推企微（同一事件 30 分钟最多一次）。
//
// 为什么不用上游的 alerting：它按 WorkBuddy 的账号模型判定"健康号数量"，
// 不认识码道。这里只发我们关心的事件，去重后直接打 webhook。
package agg

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

type notifier struct {
	webhook string
	mu      sync.Mutex
	last    map[string]time.Time
}

const notifyMinInterval = 30 * time.Minute

var (
	notifyOnce sync.Once
	notifyRef  *notifier
)

// SharedNotifier 读配置里的 alerting.webhook_url（企微机器人）。
func SharedNotifier() *notifier {
	notifyOnce.Do(func() {
		cfgPath := os.Getenv("WB_AGG_CONFIG")
		if cfgPath == "" {
			cfgPath = "/opt/wb2api-agg/config.json"
		}
		raw, err := os.ReadFile(cfgPath)
		if err != nil {
			return
		}
		var cfg struct {
			Alerting struct {
				WebhookURL string `json:"webhook_url"`
			} `json:"alerting"`
		}
		if json.Unmarshal(raw, &cfg) != nil || cfg.Alerting.WebhookURL == "" {
			return
		}
		notifyRef = &notifier{webhook: cfg.Alerting.WebhookURL, last: map[string]time.Time{}}
	})
	return notifyRef
}

// Notify 发一条；key 用于去重（例如 "signin:codearts"）。
func (n *notifier) Notify(key, content string) {
	if n == nil || n.webhook == "" {
		return
	}
	n.mu.Lock()
	if t, ok := n.last[key]; ok && time.Since(t) < notifyMinInterval {
		n.mu.Unlock()
		return
	}
	n.last[key] = time.Now()
	n.mu.Unlock()

	body, _ := json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": content},
	})
	req, err := http.NewRequest(http.MethodPost, n.webhook, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[agg] 告警推送失败: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("[agg] 告警推送返回 %d", resp.StatusCode)
	}
}

// NotifySignin 签到结果推送（只在失败时发，成功不打扰）。
func NotifySignin(name string, ok bool, summary string) {
	if ok {
		return
	}
	SharedNotifier().Notify("signin:"+name, "[网关] 签到失败："+name+"\n"+summary)
}

// NotifyCredential 凭据类故障。
func NotifyCredential(platform, account string, err error) {
	SharedNotifier().Notify("cred:"+platform+":"+account,
		"[网关] "+platform+" 凭据异常："+account+"\n"+err.Error())
}
