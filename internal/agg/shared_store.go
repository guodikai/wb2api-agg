// shared_store.go 凭据目录的单例（registry 与面板共用同一份）。
package agg

import (
	"os"
	"sync"

	"github.com/misakano7545/workbuddy2api-panel/internal/creds"
)

var (
	storeOnce sync.Once
	storeRef  *creds.Store
)

// sharedStore 读 WB_AGG_CREDS（默认 /opt/wb2api-agg/creds）。读不到返回 nil。
func sharedStore() *creds.Store {
	storeOnce.Do(func() {
		dir := os.Getenv("WB_AGG_CREDS")
		if dir == "" {
			dir = "/opt/wb2api-agg/creds"
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return
		}
		if s, err := creds.New(dir); err == nil {
			storeRef = s
		}
	})
	return storeRef
}
