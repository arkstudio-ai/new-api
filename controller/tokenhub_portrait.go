package controller

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	portrait "github.com/QuantumNous/new-api/service/tokenhubportrait"
	"github.com/gin-gonic/gin"
)

func tokenHubPortraitError(c *gin.Context, err error) {
	e := portrait.PublicError(err)
	c.JSON(e.Status, gin.H{"error": gin.H{"code": e.Code, "type": "portrait_error", "message": e.Message}})
}

func tokenHubPortraitClient(c *gin.Context) (*portrait.Client, error) {
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "" || group == "auto" {
		return nil, portrait.Fail("portrait_group_selection_required", "素材库需要使用明确的令牌分组。", 400)
	}
	var channels []*model.Channel
	q := model.ApplyChannelGroupFilter(model.DB.Where("type = ? AND status = ?", constant.ChannelTypeSora, common.ChannelStatusEnabled), group)
	if id := c.Query("channel_id"); id != "" {
		n, err := strconv.Atoi(id)
		if err != nil || n <= 0 {
			return nil, portrait.Fail("portrait_channel_unavailable", "渠道 ID 无效。", 400)
		}
		q = q.Where("id = ?", n)
	}
	if err := q.Order("priority DESC").Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, ch := range channels {
		if portrait.Enabled(ch) {
			return portrait.NewClient(ch, c.GetInt("id"), c.GetInt("token_id"), ch.Key)
		}
	}
	return nil, portrait.Fail("portrait_channel_unavailable", "当前令牌分组没有可用的 TokenHub 素材库渠道。", 400)
}

// TokenHubPortrait handles an allowlisted set of portrait operations. Asset and
// group IDs are checked against the local caller/account binding before dispatch.
// It never forwards a caller-provided ProjectName or account-global list.
func TokenHubPortrait(c *gin.Context) {
	client, err := tokenHubPortraitClient(c)
	if err != nil {
		tokenHubPortraitError(c, err)
		return
	}
	var body map[string]any
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodDelete {
		if err = common.DecodeJson(http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024), &body); err != nil || body == nil {
			tokenHubPortraitError(c, portrait.Fail("portrait_invalid_request", "需要有效的 JSON 请求体。", 400))
			return
		}
	}
	action := c.FullPath()
	id := c.Param("id")
	var result any
	switch {
	case strings.HasSuffix(action, "/session"):
		result, err = client.CreateSession(c.Request.Context(), portrait.String(body, "CallbackURL"))
	case strings.HasSuffix(action, "/validate-result"):
		result, err = client.ValidateSession(c.Request.Context(), portrait.String(body, "BytedToken"))
	case strings.HasSuffix(action, "/asset-group/list"):
		rows, e := client.ListGroups(c.Request.Context())
		err = e
		items := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			items = append(items, gin.H{"Id": row.GroupId, "GroupType": row.GroupType, "Name": row.Name})
		}
		result = gin.H{"Items": items, "Total": len(items)}
	case strings.HasSuffix(action, "/asset/list"):
		filter, _ := body["Filter"].(map[string]any)
		ids, _ := filter["GroupIds"].([]any)
		if len(ids) != 1 {
			err = portrait.Fail("portrait_group_selection_required", "查询素材列表需指定一个所属资产组 Filter.GroupIds。", 400)
			break
		}
		group, _ := ids[0].(string)
		rows, e := client.ListAssets(c.Request.Context(), group)
		err = e
		items := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			items = append(items, gin.H{"Id": row.AssetId, "GroupId": row.GroupId, "AssetType": row.AssetType, "Status": row.Status, "Name": row.Name})
		}
		result = gin.H{"Items": items, "Total": len(items)}
	case strings.HasSuffix(action, "/asset-group"):
		if kind := portrait.String(body, "GroupType"); kind != "" && kind != "AIGC" {
			err = portrait.Fail("portrait_authentication_required", "真人资产组必须通过本人活体认证创建。", 409)
			break
		}
		result, err = client.CreateGroup(c.Request.Context(), portrait.String(body, "Name"))
	case strings.HasSuffix(action, "/asset"):
		asset, e := client.EnsureAsset(c.Request.Context(), portrait.String(body, "GroupId"), portrait.String(body, "URL"), portrait.String(body, "AssetType"), 0)
		err = e
		if asset != nil {
			result = gin.H{"Id": asset.AssetId, "Status": asset.Status, "GroupId": asset.GroupId, "URI": "asset://" + asset.AssetId}
		}
	case strings.Contains(action, "/asset-group/:id"):
		if c.Request.Method == http.MethodGet {
			result, err = client.Group(c.Request.Context(), id)
			break
		}
		if _, err = client.OwnGroup(c.Request.Context(), id); err != nil {
			break
		}
		if c.Request.Method == http.MethodPut {
			if requested := portrait.String(body, "Id"); requested != "" && requested != id {
				err = portrait.Fail("portrait_invalid_request", "请求体和路径中的资产组 ID 不一致。", 400)
				break
			}
			update := map[string]any{"Id": id}
			for _, key := range []string{"Name", "Description"} {
				if value, exists := body[key]; exists {
					text, ok := value.(string)
					if !ok {
						err = portrait.Fail("portrait_invalid_request", "资产组名称和描述必须为文本。", 400)
						break
					}
					update[key] = text
				}
			}
			if err != nil {
				break
			}
			result, err = client.Call(c.Request.Context(), http.MethodPut, "asset-group/"+url.PathEscape(id), update)
			if _, provided := update["Name"]; err == nil && provided {
				err = model.DB.WithContext(c.Request.Context()).Model(&model.TokenHubPortraitGroup{}).Where("scope = ? AND group_id = ?", client.Scope, id).Update("name", portrait.String(body, "Name")).Error
			}
		} else if c.Request.Method == http.MethodDelete {
			result, err = client.Call(c.Request.Context(), http.MethodDelete, "asset-group/"+url.PathEscape(id), nil)
			// Retain a tombstone so a revoked authorization cannot be reused.
			if err == nil {
				err = model.DB.WithContext(c.Request.Context()).Model(&model.TokenHubPortraitGroup{}).Where("scope = ? AND group_id = ?", client.Scope, id).Update("revoked", true).Error
			}
		}
	case strings.Contains(action, "/asset/:id"):
		if c.Request.Method == http.MethodGet {
			result, err = client.Asset(c.Request.Context(), id)
			break
		}
		if _, err = client.Asset(c.Request.Context(), id); err != nil {
			break
		}
		if c.Request.Method == http.MethodDelete {
			result, err = client.Call(c.Request.Context(), http.MethodDelete, "asset/"+url.PathEscape(id), nil)
			if err == nil {
				err = model.DB.WithContext(c.Request.Context()).Model(&model.TokenHubPortraitAsset{}).Where("scope = ? AND asset_id = ?", client.Scope, id).Update("status", "Deleted").Error
			}
		}
	default:
		err = errors.New("unsupported portrait operation")
	}
	if err != nil {
		tokenHubPortraitError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"ResponseMetadata": gin.H{}, "Result": result})
}
