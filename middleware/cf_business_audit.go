package middleware

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// CfBusinessAudit attaches Canvas Flow business audit headers to the
// request context and softens the vendor token-quota / model-limit
// enforcement when the caller is a "platform service token" — that is,
// a token created in new-api's admin whose Name starts with
// `cf-platform-`. Such tokens are issued by the Canvas Flow platform
// once (per deployment) and stored in tenant-api's env; per-tenant
// quota / per-model access are enforced upstream in tenant-api itself,
// so vendor's token row carries unlimited quota + no model limits and
// CfBusinessAudit is what makes that the runtime reality.
//
// Headers attached (both optional — absent = empty string):
//
//	X-CF-Tenant-Id    tenant the caller belongs to (Canvas Flow tenant id)
//	X-CF-End-User     end user inside that tenant (Canvas Flow user id)
//
// Both land in gin context as `cf_tenant_id` / `cf_end_user` and are
// later picked up by the channel log writer (b.4) + the usage reporter
// (b.3) so we can attribute every channel call to a tenant + user.
//
// Mount this middleware AFTER TokenAuth() — it relies on the
// `token_name` context key TokenAuth sets.
//
// Why a name-prefix rather than a separate token table:
//   - keeps vendor admin UI / API unchanged; operator just creates a
//     normal token, types `cf-platform-default` (or any cf-platform-*
//     name) and it gets the service-token semantics
//   - upgrading vendor source code never breaks this, the prefix is the
//     entire integration surface
const (
	CfPlatformTokenNamePrefix = "cf-platform-"
	CfHeaderTenantId          = "X-CF-Tenant-Id"
	CfHeaderEndUser           = "X-CF-End-User"
	CfCtxTenantId             = "cf_tenant_id"
	CfCtxEndUser              = "cf_end_user"
	CfCtxIsServiceToken       = "cf_is_service_token"
)

func CfBusinessAudit() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Audit headers — always extracted, regardless of token kind.
		if tenantId := c.GetHeader(CfHeaderTenantId); tenantId != "" {
			c.Set(CfCtxTenantId, tenantId)
		}
		if endUser := c.GetHeader(CfHeaderEndUser); endUser != "" {
			c.Set(CfCtxEndUser, endUser)
		}

		// Service-token softening. TokenAuth already populated token_name;
		// if it's a cf-platform-* token we override quota / model-limit
		// gates so the call proceeds even though vendor's `tokens` table
		// row carries default per-token quota (set high at creation).
		tokenName, _ := c.Get("token_name")
		name, _ := tokenName.(string)
		if strings.HasPrefix(name, CfPlatformTokenNamePrefix) {
			c.Set(CfCtxIsServiceToken, true)
			c.Set("token_unlimited_quota", true)
			c.Set("token_model_limit_enabled", false)
		}
		c.Next()
	}
}
