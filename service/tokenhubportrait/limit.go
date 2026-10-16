package tokenhubportrait

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/google/uuid"
)

var portraitLimits = struct {
	sync.Mutex
	windows map[string][]time.Time
}{windows: map[string][]time.Time{}}

// Redis uses server time and a sliding window, shared by all gateway replicas.
// Without Redis, the same policy is enforced within this process.
const limitScript = `
local t = redis.call('TIME')
local now = tonumber(t[1])*1000 + math.floor(tonumber(t[2])/1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now-1000)
if redis.call('ZCARD', KEYS[1]) >= tonumber(ARGV[1]) then return 0 end
redis.call('ZADD', KEYS[1], now, ARGV[2])
redis.call('PEXPIRE', KEYS[1], 2000)
return 1
`

func (c *Client) allow(ctx context.Context, method, path string) error {
	limit := 0
	action := path
	switch {
	case path == "session" || path == "validate-result":
		limit = 3
	case path == "asset/list" || path == "asset-group/list":
		limit = 10
	case method == "GET":
		limit = 100
		action = "get"
	case method == "PUT":
		limit = 10
		action = "update"
	case method == "DELETE":
		limit = 5
		action = "delete"
	}
	if limit == 0 {
		return nil
	} // CreateAsset limits depend on upstream entitlement.
	key := "tokenhub:portrait:limit:" + Digest(c.BaseURL+"\n"+Digest(c.Key)) + ":" + action
	allowed := false
	if common.RedisEnabled {
		if common.RDB == nil {
			return Fail("portrait_limiter_unavailable", "素材库限流服务尚未就绪。", 503)
		}
		value, err := common.RDB.Eval(ctx, limitScript, []string{key}, strconv.Itoa(limit), uuid.NewString()).Int()
		if err != nil {
			return Fail("portrait_limiter_unavailable", "素材库限流服务暂时不可用。", 503)
		}
		allowed = value == 1
	} else {
		now := time.Now()
		cutoff := now.Add(-time.Second)
		portraitLimits.Lock()
		previous := portraitLimits.windows[key]
		current := previous[:0]
		for _, stamp := range previous {
			if stamp.After(cutoff) {
				current = append(current, stamp)
			}
		}
		if len(current) < limit {
			current = append(current, now)
			allowed = true
		}
		portraitLimits.windows[key] = current
		if len(portraitLimits.windows) > 1000 {
			for k, window := range portraitLimits.windows {
				if len(window) == 0 || window[len(window)-1].Before(cutoff) {
					delete(portraitLimits.windows, k)
				}
			}
		}
		portraitLimits.Unlock()
	}
	if !allowed {
		return Fail("portrait_rate_limited", "素材库请求过于频繁，请稍后查询已有状态。", 429)
	}
	return nil
}
