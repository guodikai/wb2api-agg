// js.go 提供注入到主面板的脚本。
package panelagg

import (
	_ "embed"
	"net/http"
	"strings"
	"time"
)

//go:embed panel.js
var panelJS []byte

// servePanelJS 输出注入脚本（无需鉴权：它只是一段静态代码，接口调用自带 api_key）。
func (h *Handler) servePanelJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "panel.js", time.Time{}, strings.NewReader(string(panelJS)))
}
