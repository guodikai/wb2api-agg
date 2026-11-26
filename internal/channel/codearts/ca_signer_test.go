package codearts

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// 黄金向量：由一份独立实现（Python，逐行对齐华为 SDK-HMAC-SHA256 规范）算出。
// 固定时间 + 固定凭据 + 固定请求，任何编码/排序/换行改动都会让签名变化 → 立刻红。
//
// 若将来与真号抓到的 Authorization 不一致，以真号为准更新本向量，并在注释里记来源。
func TestSignRequestGoldenVector(t *testing.T) {
	body := []byte(`{"model":"x"}`)
	req, err := http.NewRequest(http.MethodPost,
		SnapEngineApiHost+EpChatV2, strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HdrAgentType, AgentType)

	cred := SignCredential{
		AccessKeyID:     "AKIDEXAMPLE",
		SecretAccessKey: "SECRETEXAMPLE",
		SecurityToken:   "STOKENEXAMPLE",
	}
	signRequestAt(req, body, cred, time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC))

	const want = "SDK-HMAC-SHA256 Access=AKIDEXAMPLE, " +
		"SignedHeaders=agent-type;content-type;host;x-sdk-content-sha256;x-sdk-date;x-security-token, " +
		"Signature=a37ef8dcc4e228d3217adc66f35814a24c460cd5bb713ee0c4080053d846425c"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("签名不一致\n got: %s\nwant: %s", got, want)
	}
	if got := req.Header.Get("X-Sdk-Date"); got != "20261126T000000Z" {
		t.Errorf("X-Sdk-Date = %q", got)
	}
	if got := req.Header.Get("X-Sdk-Content-Sha256"); got == "" {
		t.Error("缺少 X-Sdk-Content-Sha256")
	}
}

// 无 STS 时不得发 X-Security-Token（长期 AK/SK 场景），签名头集合随之变化。
func TestSignRequestWithoutSecurityToken(t *testing.T) {
	body := []byte(`{}`)
	req, _ := http.NewRequest(http.MethodPost, SnapEngineApiHost+EpModelBuiltin, strings.NewReader("{}"))
	cred := SignCredential{AccessKeyID: "AK", SecretAccessKey: "SK"}
	signRequestAt(req, body, cred, time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC))
	if req.Header.Get("X-Security-Token") != "" {
		t.Error("无 STS 时不应设置 X-Security-Token")
	}
	if !strings.Contains(req.Header.Get("Authorization"), "SignedHeaders=") {
		t.Fatal("Authorization 缺少 SignedHeaders")
	}
	if strings.Contains(req.Header.Get("Authorization"), "x-security-token") {
		t.Error("SignedHeaders 不应包含 x-security-token")
	}
}

// 业务头必须在签名前设置：签名覆盖全部请求头，漏了就会被服务端判签名不符。
func TestSignRequestCoversAllHeaders(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, SnapEngineApiHost+EpCurrentUser, nil)
	req.Header.Set("X-Language", LangZHCN)
	signRequestAt(req, nil, SignCredential{AccessKeyID: "AK", SecretAccessKey: "SK"},
		time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC))
	auth := req.Header.Get("Authorization")
	for _, h := range []string{"x-language", "host", "x-sdk-date", "x-sdk-content-sha256"} {
		if !strings.Contains(auth, h) {
			t.Errorf("SignedHeaders 缺少 %s: %s", h, auth)
		}
	}
}

func TestCanonicalURITrailingSlashAndEscape(t *testing.T) {
	cases := map[string]string{
		"/api/v2/chat/completions": "/api/v2/chat/completions/",
		"/v1/chat/chat":            "/v1/chat/chat/",
		"/a b/c":                   "/a%20b/c/",
	}
	for in, want := range cases {
		if got := canonicalURI(in); got != want {
			t.Errorf("canonicalURI(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanonicalQuerySortsKeysAndValues(t *testing.T) {
	got := canonicalQuery("b=2&a=3&a=1")
	want := "a=1&a=3&b=2"
	if got != want {
		t.Errorf("canonicalQuery = %q, want %q", got, want)
	}
	if canonicalQuery("") != "" {
		t.Error("空查询串应为空")
	}
}
