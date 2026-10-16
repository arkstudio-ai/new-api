package relay

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	portrait "github.com/QuantumNous/new-api/service/tokenhubportrait"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPortraitRefusalPrecedesVideoBillingAndDispatch(t *testing.T) {
	for _, scenario := range []string{"asset-not-owned", "asset-failed"} {
		t.Run(scenario, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{})
			require.NoError(t, err)
			previousDB, previousRedis := model.DB, common.RedisEnabled
			model.DB = db
			common.RedisEnabled = false
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			t.Cleanup(func() { model.DB = previousDB; common.RedisEnabled = previousRedis; sqlDB.Close() })
			// Billing tables deliberately do not exist: reaching billing would fail.
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.TokenHubPortraitGroup{}, &model.TokenHubPortraitAsset{}))
			dispatched := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var result map[string]any
				if strings.Contains(r.URL.Path, "asset-group/") {
					result = map[string]any{"Id": "verified-group", "GroupType": "AIGC", "Status": "active"}
				} else if strings.Contains(r.URL.Path, "asset/") {
					result = map[string]any{"Id": "verified-asset", "GroupId": "verified-group", "AssetType": "Video", "Status": "Failed"}
				} else {
					dispatched++
				}
				raw, _ := common.Marshal(map[string]any{"ResponseMetadata": map[string]any{}, "Result": result})
				w.Write(raw)
			}))
			defer upstream.Close()
			base := upstream.URL
			settings := dto.ChannelOtherSettings{TokenHubPortraitEnabled: true}
			ch := model.Channel{Id: 128, Type: constant.ChannelTypeSora, Key: "test-key", BaseURL: &base}
			ch.SetOtherSettings(settings)
			require.NoError(t, db.Create(&ch).Error)
			metadata := map[string]any{"content": []any{map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "asset://verified-asset"}}}}
			expected := "portrait_asset_not_found"
			if scenario == "asset-failed" {
				expected = "portrait_asset_failed"
				metadata["portrait_group_id"] = "verified-group"
				client, err := portrait.NewClient(&ch, 1, 7, "")
				require.NoError(t, err)
				require.NoError(t, db.Create(&model.TokenHubPortraitGroup{BindingKey: "binding", Scope: client.Scope, GroupId: "verified-group", GroupType: "AIGC"}).Error)
				require.NoError(t, db.Create(&model.TokenHubPortraitAsset{CacheKey: "cached", Scope: client.Scope, GroupId: "verified-group", AssetId: "verified-asset", AssetType: "Video", Status: "Active", Name: "cached"}).Error)
			}
			raw, err := common.Marshal(map[string]any{"model": "doubao-seedance-2-0-260128", "prompt": "tea portrait", "metadata": metadata})
			require.NoError(t, err)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewReader(raw))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set("platform", strconv.Itoa(constant.ChannelTypeSora))
			common.SetContextKey(ctx, constant.ContextKeyChannelId, ch.Id)
			common.SetContextKey(ctx, constant.ContextKeyChannelType, ch.Type)
			common.SetContextKey(ctx, constant.ContextKeyChannelKey, ch.Key)
			common.SetContextKey(ctx, constant.ContextKeyChannelBaseUrl, base)
			common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, settings)
			info := &relaycommon.RelayInfo{UserId: 1, TokenId: 7, OriginModelName: "doubao-seedance-2-0-260128", TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
			result, taskErr := RelayTaskSubmit(ctx, info)
			require.Nil(t, result)
			require.NotNil(t, taskErr)
			require.Equal(t, expected, taskErr.Code)
			require.Equal(t, 400, taskErr.StatusCode)
			require.Nil(t, info.Billing)
			require.Zero(t, info.FinalPreConsumedQuota)
			require.Empty(t, info.PublicTaskID)
			require.Zero(t, dispatched)
		})
	}
}
