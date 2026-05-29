// Package volcasset 实现火山方舟 Asset（素材资产）OpenAPI 的鉴权签名与转发。
//
// 火山 Asset API 走通用 OpenAPI 网关（open.volcengineapi.com），使用 AK/SK 的
// 火山签名（"HMAC-SHA256"，与即梦 relay/channel/jimeng/sign.go 同算法，仅
// region/service 不同）。本包供 controller 在收到下游 new-api Bearer 请求后，
// 用渠道配置的火山 AK/SK 重新签名并转发到上游。
package volcasset

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/service"
)

const (
	volcOpenAPIHost = "open.volcengineapi.com"
	volcRegion      = "cn-beijing"
	volcService     = "ark"
	volcVersion     = "2024-01-01"
)

// 支持的 Asset Action 白名单，防止下游借转发调用任意火山 OpenAPI。
var allowedActions = map[string]bool{
	"CreateAssetGroup": true,
	"CreateAsset":      true,
	"ListAssets":       true,
	"ListAssetGroups":  true,
	"GetAsset":         true,
	"GetAssetGroup":    true,
	"UpdateAssetGroup": true,
	"UpdateAsset":      true,
}

// IsAllowedAction 返回该 Action 是否在 Asset 转发白名单内。
func IsAllowedAction(action string) bool {
	return allowedActions[action]
}

// CallResult 封装火山 OpenAPI 调用结果，供 controller 透传给客户端。
type CallResult struct {
	StatusCode int
	Body       []byte
}

// Call 使用渠道的 AK/SK 调用火山 Asset OpenAPI（Action 风格）。
// body 为下游透传的 JSON 请求体；proxy 为渠道代理（可空）。
func Call(ctx context.Context, ak, sk, action string, body []byte, proxy string) (*CallResult, error) {
	if !IsAllowedAction(action) {
		return nil, fmt.Errorf("unsupported volc asset action: %s", action)
	}
	if strings.TrimSpace(ak) == "" || strings.TrimSpace(sk) == "" {
		return nil, fmt.Errorf("volc access key / secret key not configured on channel")
	}
	if body == nil {
		body = []byte("{}")
	}

	requestURL := fmt.Sprintf("https://%s/?Action=%s&Version=%s",
		volcOpenAPIHost, url.QueryEscape(action), url.QueryEscape(volcVersion))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new volc asset request failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if err := signRequest(req, body, ak, sk); err != nil {
		return nil, fmt.Errorf("sign volc asset request failed: %w", err)
	}

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new volc asset http client failed: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do volc asset request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read volc asset response failed: %w", err)
	}

	return &CallResult{StatusCode: resp.StatusCode, Body: respBody}, nil
}

// signRequest 对请求执行火山引擎签名（V4 风格 HMAC-SHA256）。
func signRequest(req *http.Request, bodyBytes []byte, accessKey, secretKey string) error {
	u := req.URL
	header := req.Header

	payloadHash := sha256.Sum256(bodyBytes)
	hexPayloadHash := hex.EncodeToString(payloadHash[:])

	t := time.Now().UTC()
	xDate := t.Format("20060102T150405Z")
	shortDate := t.Format("20060102")

	host := u.Host
	header.Set("Host", host)
	header.Set("X-Date", xDate)
	header.Set("X-Content-Sha256", hexPayloadHash)
	if header.Get("Content-Type") == "" {
		header.Set("Content-Type", "application/json")
	}

	// Canonical query string: 按 key 排序后 url 编码。
	queryParams := u.Query()
	sortedKeys := make([]string, 0, len(queryParams))
	for k := range queryParams {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	var queryParts []string
	for _, k := range sortedKeys {
		values := queryParams[k]
		sort.Strings(values)
		for _, v := range values {
			queryParts = append(queryParts, fmt.Sprintf("%s=%s", url.QueryEscape(k), url.QueryEscape(v)))
		}
	}
	canonicalQueryString := strings.Join(queryParts, "&")

	headersToSign := map[string]string{
		"host":             host,
		"x-date":           xDate,
		"x-content-sha256": hexPayloadHash,
		"content-type":     header.Get("Content-Type"),
	}
	signedHeaderKeys := make([]string, 0, len(headersToSign))
	for k := range headersToSign {
		signedHeaderKeys = append(signedHeaderKeys, k)
	}
	sort.Strings(signedHeaderKeys)

	var canonicalHeaders strings.Builder
	for _, k := range signedHeaderKeys {
		canonicalHeaders.WriteString(k)
		canonicalHeaders.WriteString(":")
		canonicalHeaders.WriteString(strings.TrimSpace(headersToSign[k]))
		canonicalHeaders.WriteString("\n")
	}
	signedHeaders := strings.Join(signedHeaderKeys, ";")

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		u.Path,
		canonicalQueryString,
		canonicalHeaders.String(),
		signedHeaders,
		hexPayloadHash,
	)

	hashedCanonicalRequest := sha256.Sum256([]byte(canonicalRequest))
	hexHashedCanonicalRequest := hex.EncodeToString(hashedCanonicalRequest[:])

	credentialScope := fmt.Sprintf("%s/%s/%s/request", shortDate, volcRegion, volcService)
	stringToSign := fmt.Sprintf("HMAC-SHA256\n%s\n%s\n%s",
		xDate,
		credentialScope,
		hexHashedCanonicalRequest,
	)

	kDate := hmacSHA256([]byte(secretKey), []byte(shortDate))
	kRegion := hmacSHA256(kDate, []byte(volcRegion))
	kService := hmacSHA256(kRegion, []byte(volcService))
	kSigning := hmacSHA256(kService, []byte("request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	authorization := fmt.Sprintf("HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey,
		credentialScope,
		signedHeaders,
		signature,
	)
	header.Set("Authorization", authorization)
	return nil
}

func hmacSHA256(key []byte, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
