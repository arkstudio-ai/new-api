// Package tokenhubportrait implements TokenHub's Bearer-authenticated portrait
// API. It never uses the Volcengine AK/SK API or shares assets across scopes.
package tokenhubportrait

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

// TokenHub forwards this to Ark's resource project. The supplied integration
// document contradicts its own default-project example; live AIGC creation
// rejects the company display name with InvalidParameter.ProjectName.
const DefaultProject = "default"

// Error contains reviewed public diagnostics, not arbitrary upstream messages.
type Error struct {
	Code     string
	Message  string
	Status   int
	Rejected bool
}

func (e *Error) Error() string { return e.Message }
func Fail(code, message string, status int) *Error {
	return &Error{Code: code, Message: message, Status: status}
}
func PublicError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Fail("portrait_service_error", "素材库服务暂时不可用，请查询已有素材状态后再继续。", http.StatusBadGateway)
}

func Digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

type Client struct {
	BaseURL, Key, Project, Scope string
	HTTP                         *http.Client
	// Tests can replace content hashing without making private URLs permissible.
	SourceDigest func(context.Context, string) (string, error)
}

func Enabled(ch *model.Channel) bool {
	return ch != nil && ch.Type == constant.ChannelTypeSora && ch.GetOtherSettings().TokenHubPortraitEnabled
}

func NewClient(ch *model.Channel, userID, tokenID int, key string) (*Client, error) {
	if !Enabled(ch) || userID <= 0 || tokenID <= 0 {
		return nil, Fail("portrait_channel_unavailable", "当前渠道未启用 TokenHub 素材库。", 400)
	}
	if ch.ChannelInfo.IsMultiKey {
		return nil, Fail("portrait_account_configuration", "素材库渠道需使用单个上游账户密钥。", 400)
	}
	if key == "" {
		key = ch.Key
	}
	if strings.TrimSpace(key) == "" {
		return nil, Fail("portrait_account_configuration", "素材库渠道未配置上游密钥。", 400)
	}
	project := strings.TrimSpace(ch.GetOtherSettings().TokenHubPortraitProject)
	if project == "" {
		project = DefaultProject
	}
	if ch.BaseURL == nil || strings.TrimSpace(*ch.BaseURL) == "" {
		return nil, Fail("portrait_account_configuration", "素材库渠道需明确配置 TokenHub 地址。", 400)
	}
	base := strings.TrimSuffix(strings.TrimRight(ch.GetBaseURL(), "/"), "/v1")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, Fail("portrait_account_configuration", "素材库渠道地址配置无效。", 400)
	}
	hc, err := service.GetHttpClientWithProxy(ch.GetSetting().Proxy)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	// Do not follow redirects with the TokenHub bearer credential.
	clone := *hc
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	scope := Digest(strconv.Itoa(userID) + "\n" + strconv.Itoa(tokenID) + "\n" + strconv.Itoa(ch.Id) + "\n" + base + "\n" + Digest(key) + "\n" + project)
	c := &Client{BaseURL: base, Key: key, Project: project, Scope: scope, HTTP: &clone}
	c.SourceDigest = hashSource
	return c, nil
}

var safeCode = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,120}$`)

func (c *Client) Call(ctx context.Context, method, path string, body map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := c.allow(ctx, method, path); err != nil {
		return nil, err
	}
	payload := make(map[string]any, len(body)+1)
	for k, v := range body {
		payload[k] = v
	}
	payload["ProjectName"] = c.Project
	if method == http.MethodGet || method == http.MethodDelete {
		parts := strings.SplitN(path, "/", 2)
		if len(parts) == 2 && (parts[0] == "asset" || parts[0] == "asset-group") {
			id, err := url.PathUnescape(parts[1])
			if err != nil {
				return nil, err
			}
			// The relay's path does not populate Ark's JSON Id automatically.
			payload["Id"] = id
		}
	}
	endpoint := c.BaseURL + "/v1/portrait/" + path
	// Live TokenHub requires JSON even for GET: a query-only request returns
	// invalid_json / unexpected end of JSON input (verified 2026-10-16).
	rawRequest, err := common.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(rawRequest))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, Fail("portrait_upstream_outcome_unknown", "素材库请求结果未确认，请查询已有记录，不要重复上传。", 502)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return nil, Fail("portrait_upstream_outcome_unknown", "素材库响应未能完整读取，请查询已有记录。", 502)
	}
	var envelope struct {
		Result   map[string]any `json:"Result"`
		Metadata struct {
			Error struct {
				Code string `json:"Code"`
			} `json:"Error"`
		} `json:"ResponseMetadata"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if common.Unmarshal(raw, &envelope) != nil {
		return nil, Fail("portrait_upstream_invalid_response", "素材库返回了无法识别的响应。", 502)
	}
	code := envelope.Metadata.Error.Code
	if code == "" {
		code = envelope.Error.Code
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || code != "" {
		if !safeCode.MatchString(code) {
			code = "upstream_error"
		}
		status := 502
		if resp.StatusCode == 429 {
			status = 429
		}
		e := Fail("portrait_upstream_"+code, "素材库请求未完成（"+code+"），请核对项目配置、素材库权益或素材状态。", status)
		e.Rejected = (resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 408) || (resp.StatusCode == 200 && code != "upstream_error")
		if String(envelope.Result, "Id") != "" {
			e.Rejected = false
		}
		return nil, e
	}
	if envelope.Result == nil {
		return nil, Fail("portrait_upstream_invalid_response", "素材库响应缺少 Result。", 502)
	}
	return envelope.Result, nil
}

func String(m map[string]any, k string) string { s, _ := m[k].(string); return s }

func hashSource(ctx context.Context, source string) (string, error) {
	protection := &common.SSRFProtection{DomainFilterMode: false, IpFilterMode: false, ApplyIPFilterForDomain: true, AllowedPorts: []int{80, 443}}
	if err := protection.ValidateURL(source); err != nil {
		return "", Fail("portrait_source_url_invalid", "素材需要可公开访问的 HTTP/HTTPS 文件地址。", 400)
	}
	u, err := url.Parse(source)
	if err != nil || u.User != nil || u.Fragment != "" {
		return "", Fail("portrait_source_url_invalid", "素材地址无效。", 400)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// A dedicated client does not send API credentials to source media hosts.
	transport := &http.Transport{ResponseHeaderTimeout: 10 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		// Check the addresses actually used by the socket, closing DNS-rebinding
		// and private-IP redirects even when global fetch protection is disabled.
		for _, ip := range ips {
			if !protection.IsIPAccessAllowed(ip.IP) {
				return nil, fmt.Errorf("source address is not public")
			}
		}
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		for _, ip := range ips {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			err = dialErr
		}
		if err == nil {
			err = fmt.Errorf("source has no public address")
		}
		return nil, err
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return protection.ValidateURL(req.URL.String())
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", Fail("portrait_source_unavailable", "无法读取素材文件，请检查链接是否已过期。", 400)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", Fail("portrait_source_unavailable", "素材文件下载失败，请检查链接是否已过期。", 400)
	}
	const maxBytes = 100 * 1024 * 1024
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || n == 0 || n > maxBytes {
		return "", Fail("portrait_source_invalid", "素材文件为空、读取失败或超过 100 MiB。", 400)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
