// 华为云 SDK-HMAC-SHA256 请求签名（AK/SK + 可选 STS security token）。
//
// 移植来源：HITZY2002/codearts2api internal/upstream/signer.go（MIT）。
// 对齐 @huaweicloud/huaweicloud-sdk-core 的 AKSKSigner：
//   - signed headers = 请求里全部头（小写排序），含 x-sdk-content-sha256
//   - CanonicalURI 每段 percent-encode 且末尾补 "/"
//   - payload hash 取 X-Sdk-Content-Sha256 头
//
// 与源的唯一差别：把 time.Now() 抽成参数（signRequestAt），这样签名可以离线
// 用固定时间做黄金向量断言——签名错了在 CI 就红，不用等真号 403 才发现。
package codearts

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// SigningAlgorithm 签名算法标识。
const SigningAlgorithm = "SDK-HMAC-SHA256"

// SignCredential AK/SK 临时凭证（由 OAuth 换取的 STS 下发）。
type SignCredential struct {
	AccessKeyID     string
	SecretAccessKey string
	SecurityToken   string
}

// SignRequest 给请求加 X-Sdk-Date / X-Security-Token / X-Sdk-Content-Sha256 /
// Authorization 头。body 为请求体（用于 payload hash）。调用前请把业务头
// （Content-Type / Agent-Type / X-Language 等）都设置好——签名覆盖全部请求头。
func SignRequest(req *http.Request, body []byte, cred SignCredential) {
	signRequestAt(req, body, cred, time.Now())
}

// signRequestAt 同 SignRequest，但时间由调用方给出（测试用）。
func signRequestAt(req *http.Request, body []byte, cred SignCredential, now time.Time) {
	xDate := now.UTC().Format("20060102T150405Z")
	req.Header.Set("X-Sdk-Date", xDate)
	req.Header.Set("Host", req.URL.Host)
	if cred.SecurityToken != "" {
		req.Header.Set("X-Security-Token", cred.SecurityToken)
	}
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Sdk-Content-Sha256", payloadHash)

	canonicalHeaderBlock, signedHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.EscapedPath()),
		canonicalQuery(req.URL.RawQuery),
		canonicalHeaderBlock,
		signedHeaders,
		payloadHash,
	}, "\n")
	stringToSign := strings.Join([]string{
		SigningAlgorithm,
		xDate,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signature := hmacHex(cred.SecretAccessKey, stringToSign)
	req.Header.Set("Authorization", SigningAlgorithm+
		" Access="+cred.AccessKeyID+
		", SignedHeaders="+signedHeaders+
		", Signature="+signature)
}

// canonicalHeaders 全部请求头（小写 key 排序），值原样（仅去首尾空白）。
func canonicalHeaders(req *http.Request) (headers, signed string) {
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		keys = append(keys, strings.ToLower(k))
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		v := req.Header.Get(k)
		sb.WriteString(k + ":" + strings.TrimSpace(v) + "\n")
	}
	return sb.String(), strings.Join(keys, ";")
}

// canonicalURI 每段 percent-encode 且末尾补 "/"（对齐官方 SDK 的 CanonicalURI）。
func canonicalURI(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		segments[i] = url.PathEscape(seg)
	}
	out := strings.Join(segments, "/")
	if !strings.HasSuffix(out, "/") {
		out += "/"
	}
	return out
}

// canonicalQuery 规范查询串（key 与同 key 的 value 都排序）。
func canonicalQuery(raw string) string {
	if raw == "" {
		return ""
	}
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		vs := vals[k]
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacHex(key, msg string) string {
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(msg))
	return hex.EncodeToString(mac.Sum(nil))
}
