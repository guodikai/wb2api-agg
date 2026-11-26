// reqlog_wire.go 日志相关的辅助类型与单例。
package agg

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

var (
	reqLogOnce sync.Once
	reqLogRef  *ReqLog
)

// sharedReqLog 日志目录：WB_AGG_DATA（默认 /opt/wb2api-agg/data）。
func sharedReqLog() *ReqLog {
	reqLogOnce.Do(func() {
		dir := os.Getenv("WB_AGG_DATA")
		if dir == "" {
			dir = "/opt/wb2api-agg/data"
		}
		if l, err := NewReqLog(dir); err == nil {
			reqLogRef = l
		}
	})
	return reqLogRef
}

func (m *Mux) logEvent(e ReqEvent) {
	if m.ReqLog != nil {
		m.ReqLog.Record(e)
	}
}

// statusWriter 记录状态码与写出字节数。
type statusWriter struct {
	http.ResponseWriter
	code  int
	bytes int64
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		if i := strings.IndexByte(v, ','); i > 0 {
			return strings.TrimSpace(v[:i])
		}
		return strings.TrimSpace(v)
	}
	host := r.RemoteAddr
	if i := strings.LastIndexByte(host, ':'); i > 0 {
		host = host[:i]
	}
	return host
}

func outcomeOf(code int) string {
	switch {
	case code < 300:
		return "ok"
	case code < 500:
		return "client_error"
	default:
		return "server_error"
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

var _ = fmt.Sprintf

// SharedReqLog 供面板等外部包读取同一份日志。
func SharedReqLog() *ReqLog { return sharedReqLog() }
