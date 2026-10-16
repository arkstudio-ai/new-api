package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPortraitRoutesRequireBearerAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	SetTokenHubPortraitRouter(r)
	for _, target := range []struct{ method, path string }{{"POST", "/v1/portrait/session"}, {"POST", "/v1/portrait/asset"}, {"GET", "/v1/portrait/asset/asset-private"}, {"DELETE", "/v1/portrait/asset-group/group-private"}} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(target.method, target.path, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
}
