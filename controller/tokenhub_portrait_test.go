package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	portrait "github.com/QuantumNous/new-api/service/tokenhubportrait"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPortraitAPIAccountIsolationAndProjectInjection(t *testing.T) {
	previousDB, previousLog := model.DB, model.LOG_DB
	t.Cleanup(func() { model.DB, model.LOG_DB = previousDB, previousLog })
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.TokenHubPortraitSession{}, &model.TokenHubPortraitGroup{}, &model.TokenHubPortraitAsset{}))
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		require.Equal(t, "Bearer test-upstream-key", r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &body))
		require.Equal(t, portrait.DefaultProject, body["ProjectName"])
		raw, _ := common.Marshal(gin.H{"ResponseMetadata": gin.H{}, "Result": gin.H{"BytedToken": "test-liveness-token", "H5Link": "https://h5.example.com/verification"}})
		w.Write(raw)
	}))
	defer upstream.Close()
	base := upstream.URL
	ch := model.Channel{Id: 128, Type: constant.ChannelTypeSora, Status: common.ChannelStatusEnabled, Key: "test-upstream-key", BaseURL: &base, Group: "default"}
	ch.SetOtherSettings(dto.ChannelOtherSettings{TokenHubPortraitEnabled: true})
	require.NoError(t, db.Create(&ch).Error)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", 1)
		c.Set("token_id", 7)
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	})
	r.POST("/v1/portrait/session", TokenHubPortrait)
	r.POST("/v1/portrait/validate-result", TokenHubPortrait)
	r.POST("/v1/portrait/asset-group", TokenHubPortrait)
	r.GET("/v1/portrait/asset-group/:id", TokenHubPortrait)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		return rec
	}
	rec := request(http.MethodPost, "/v1/portrait/session?channel_id=128", `{"ProjectName":"attacker-project"}`)
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "https://h5.example.com/verification")
	require.Equal(t, 1, upstreamCalls)
	rec = request(http.MethodGet, "/v1/portrait/asset-group/group-foreign", "")
	require.Equal(t, 404, rec.Code)
	rec = request(http.MethodPost, "/v1/portrait/validate-result", `{"BytedToken":"another-users-token"}`)
	require.Equal(t, 404, rec.Code)
	rec = request(http.MethodPost, "/v1/portrait/asset-group", `{"Name":"real person","GroupType":"LivenessFace"}`)
	require.Equal(t, 409, rec.Code)
	rec = request(http.MethodPost, "/v1/portrait/session?channel_id=999", `{}`)
	require.Equal(t, 400, rec.Code)
	require.Equal(t, 1, upstreamCalls, "unauthorized operations must not reach the upstream account")
}

func TestPortraitTaskErrorsCannotRetryAcrossAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{TokenHubPortraitEnabled: true})
	for _, status := range []int{400, 409, 429, 500, 502} {
		require.False(t, shouldRetryTaskRelay(ctx, 128, &dto.TaskError{StatusCode: status}, 3))
	}
}
