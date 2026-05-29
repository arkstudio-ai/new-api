package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/volcasset"

	"github.com/gin-gonic/gin"
)

// 火山方舟 Asset（素材资产）管理接口转发。
//
// 对外兼容火山原生 /open/<Action> 风格，使用 new-api Bearer 令牌鉴权；
// 网关侧按令牌分组选取火山渠道，用渠道的 AK/SK 重新签名转发到上游，
// 并按令牌维度维护素材归属（隔离不同令牌的素材库）。

// volcResultBody 用于从火山响应中提取关键字段（Id / Items）。
type volcResultBody struct {
	Result struct {
		Id    string           `json:"Id"`
		Items []map[string]any `json:"Items"`
	} `json:"Result"`
}

// volcAssetRequestBody 用于从下游请求体提取归属/校验所需字段。
type volcAssetRequestBody struct {
	Id          string `json:"Id"`
	GroupId     string `json:"GroupId"`
	Name        string `json:"Name"`
	ProjectName string `json:"ProjectName"`
}

func volcAssetError(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{
		"success": false,
		"error": gin.H{
			"message": message,
			"type":    "new_api_error",
		},
	})
}

// volcAssetCall 选取渠道并用其 AK/SK 转发到火山 OpenAPI。
// 返回选中的渠道与调用结果；失败时已写入错误响应并返回 ok=false。
func volcAssetCall(c *gin.Context, action string, body []byte) (*model.Channel, *volcasset.CallResult, bool) {
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	channel, err := model.GetVolcAssetChannelByGroup(group)
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, err.Error())
		return nil, nil, false
	}
	auth := model.GetVolcAssetChannelAuth(channel)
	proxy := channel.GetSetting().Proxy
	// 请求体未带 ProjectName 时，注入渠道配置的默认项目名（带了则用调用方的）。
	body = injectDefaultProjectName(body, auth.ProjectName)
	result, err := volcasset.Call(c.Request.Context(), auth.AccessKey, auth.SecretKey, action, body, proxy)
	if err != nil {
		volcAssetError(c, http.StatusInternalServerError, err.Error())
		return nil, nil, false
	}
	return channel, result, true
}

// injectDefaultProjectName 在请求体缺少（或为空）ProjectName 时写入渠道默认值 def。
// 解析失败或 def 为空时原样返回，保证不破坏既有请求。
func injectDefaultProjectName(body []byte, def string) []byte {
	if strings.TrimSpace(def) == "" {
		return body
	}
	var m map[string]any
	if len(body) == 0 {
		m = map[string]any{}
	} else if err := common.Unmarshal(body, &m); err != nil || m == nil {
		return body
	}
	if pn, ok := m["ProjectName"].(string); ok && strings.TrimSpace(pn) != "" {
		return body // 调用方已显式指定，尊重之
	}
	m["ProjectName"] = def
	out, err := common.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// effectiveProjectName 返回用于本地归属记录的项目名：优先请求体值，否则取渠道默认。
func effectiveProjectName(reqProjectName string, channel *model.Channel) string {
	if strings.TrimSpace(reqProjectName) != "" {
		return reqProjectName
	}
	if channel != nil {
		return model.GetVolcAssetChannelAuth(channel).ProjectName
	}
	return reqProjectName
}

func volcAssetIsSuccess(result *volcasset.CallResult) bool {
	return result.StatusCode >= 200 && result.StatusCode < 300
}

func writeVolcRaw(c *gin.Context, result *volcasset.CallResult) {
	c.Data(result.StatusCode, "application/json; charset=utf-8", result.Body)
}

func parseVolcResult(body []byte) volcResultBody {
	var parsed volcResultBody
	_ = common.Unmarshal(body, &parsed)
	return parsed
}

func parseVolcRequestBody(body []byte) volcAssetRequestBody {
	var parsed volcAssetRequestBody
	_ = common.Unmarshal(body, &parsed)
	return parsed
}

// ============================
// Asset Group
// ============================

// VolcCreateAssetGroup POST /open/CreateAssetGroup
func VolcCreateAssetGroup(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	channel, result, ok := volcAssetCall(c, "CreateAssetGroup", body)
	if !ok {
		return
	}
	if volcAssetIsSuccess(result) {
		res := parseVolcResult(result.Body)
		if res.Result.Id != "" {
			req := parseVolcRequestBody(body)
			_ = model.RecordVolcAssetGroup(c.GetInt("token_id"), c.GetInt("id"), channel.Id,
				res.Result.Id, req.Name, effectiveProjectName(req.ProjectName, channel))
		}
	}
	writeVolcRaw(c, result)
}

// VolcListAssetGroups POST /open/ListAssetGroups
// 转发火山获取完整结构后，按当前令牌归属过滤 Items。
func VolcListAssetGroups(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	_, result, ok := volcAssetCall(c, "ListAssetGroups", body)
	if !ok {
		return
	}
	if volcAssetIsSuccess(result) {
		filtered := filterVolcItemsByOwnership(c, result.Body, false)
		if filtered != nil {
			writeVolcRaw(c, &volcasset.CallResult{StatusCode: result.StatusCode, Body: filtered})
			return
		}
	}
	writeVolcRaw(c, result)
}

// VolcGetAssetGroup POST /open/GetAssetGroup
func VolcGetAssetGroup(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	req := parseVolcRequestBody(body)
	if !volcAssetEnsureGroupOwned(c, req.GroupId, req.Id) {
		return
	}
	_, result, ok := volcAssetCall(c, "GetAssetGroup", body)
	if !ok {
		return
	}
	writeVolcRaw(c, result)
}

// VolcUpdateAssetGroup POST /open/UpdateAssetGroup
func VolcUpdateAssetGroup(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	req := parseVolcRequestBody(body)
	if !volcAssetEnsureGroupOwned(c, req.GroupId, req.Id) {
		return
	}
	_, result, ok := volcAssetCall(c, "UpdateAssetGroup", body)
	if !ok {
		return
	}
	if volcAssetIsSuccess(result) {
		groupId := req.Id
		if groupId == "" {
			groupId = req.GroupId
		}
		_ = model.UpdateVolcAssetGroupMeta(c.GetInt("token_id"), groupId, req.Name)
	}
	writeVolcRaw(c, result)
}

// VolcDeleteAssetGroup POST /open/DeleteAssetGroup
func VolcDeleteAssetGroup(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	req := parseVolcRequestBody(body)
	if !volcAssetEnsureGroupOwned(c, req.GroupId, req.Id) {
		return
	}
	_, result, ok := volcAssetCall(c, "DeleteAssetGroup", body)
	if !ok {
		return
	}
	if volcAssetIsSuccess(result) {
		groupId := req.Id
		if groupId == "" {
			groupId = req.GroupId
		}
		_ = model.DeleteVolcAssetGroupByToken(c.GetInt("token_id"), groupId)
	}
	writeVolcRaw(c, result)
}

// ============================
// Asset
// ============================

// VolcCreateAsset POST /open/CreateAsset
func VolcCreateAsset(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	req := parseVolcRequestBody(body)
	// 必须往归属当前令牌的素材组里创建资产，防止借他人组写入。
	if req.GroupId == "" {
		volcAssetError(c, http.StatusBadRequest, "GroupId is required")
		return
	}
	owned, ownErr := model.IsVolcAssetGroupOwnedByToken(c.GetInt("token_id"), req.GroupId)
	if ownErr != nil {
		volcAssetError(c, http.StatusInternalServerError, ownErr.Error())
		return
	}
	if !owned {
		volcAssetError(c, http.StatusForbidden, "asset group does not belong to current token")
		return
	}
	channel, result, ok := volcAssetCall(c, "CreateAsset", body)
	if !ok {
		return
	}
	if volcAssetIsSuccess(result) {
		res := parseVolcResult(result.Body)
		if res.Result.Id != "" {
			_ = model.RecordVolcAsset(c.GetInt("token_id"), c.GetInt("id"), channel.Id,
				res.Result.Id, req.GroupId, req.Name, "Image", effectiveProjectName(req.ProjectName, channel))
		}
	}
	writeVolcRaw(c, result)
}

// VolcListAssets POST /open/ListAssets
func VolcListAssets(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	_, result, ok := volcAssetCall(c, "ListAssets", body)
	if !ok {
		return
	}
	if volcAssetIsSuccess(result) {
		filtered := filterVolcItemsByOwnership(c, result.Body, true)
		if filtered != nil {
			writeVolcRaw(c, &volcasset.CallResult{StatusCode: result.StatusCode, Body: filtered})
			return
		}
	}
	writeVolcRaw(c, result)
}

// VolcGetAsset POST /open/GetAsset
func VolcGetAsset(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	req := parseVolcRequestBody(body)
	if req.Id == "" {
		volcAssetError(c, http.StatusBadRequest, "Id is required")
		return
	}
	owned, ownErr := model.IsVolcAssetOwnedByToken(c.GetInt("token_id"), req.Id)
	if ownErr != nil {
		volcAssetError(c, http.StatusInternalServerError, ownErr.Error())
		return
	}
	if !owned {
		volcAssetError(c, http.StatusForbidden, "asset does not belong to current token")
		return
	}
	_, result, ok := volcAssetCall(c, "GetAsset", body)
	if !ok {
		return
	}
	// 顺带刷新本地状态，便于后续 List 展示。
	if volcAssetIsSuccess(result) {
		res := parseVolcResult(result.Body)
		_ = res // status 同步可在后续扩展，这里仅透传
	}
	writeVolcRaw(c, result)
}

// VolcUpdateAsset POST /open/UpdateAsset
func VolcUpdateAsset(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	req := parseVolcRequestBody(body)
	if req.Id == "" {
		volcAssetError(c, http.StatusBadRequest, "Id is required")
		return
	}
	owned, ownErr := model.IsVolcAssetOwnedByToken(c.GetInt("token_id"), req.Id)
	if ownErr != nil {
		volcAssetError(c, http.StatusInternalServerError, ownErr.Error())
		return
	}
	if !owned {
		volcAssetError(c, http.StatusForbidden, "asset does not belong to current token")
		return
	}
	_, result, ok := volcAssetCall(c, "UpdateAsset", body)
	if !ok {
		return
	}
	if volcAssetIsSuccess(result) {
		_ = model.UpdateVolcAssetMeta(c.GetInt("token_id"), req.Id, req.Name, "")
	}
	writeVolcRaw(c, result)
}

// VolcDeleteAsset POST /open/DeleteAsset
func VolcDeleteAsset(c *gin.Context) {
	body, err := c.GetRawData()
	if err != nil {
		volcAssetError(c, http.StatusBadRequest, "failed to read request body")
		return
	}
	req := parseVolcRequestBody(body)
	if req.Id == "" {
		volcAssetError(c, http.StatusBadRequest, "Id is required")
		return
	}
	owned, ownErr := model.IsVolcAssetOwnedByToken(c.GetInt("token_id"), req.Id)
	if ownErr != nil {
		volcAssetError(c, http.StatusInternalServerError, ownErr.Error())
		return
	}
	if !owned {
		volcAssetError(c, http.StatusForbidden, "asset does not belong to current token")
		return
	}
	_, result, ok := volcAssetCall(c, "DeleteAsset", body)
	if !ok {
		return
	}
	if volcAssetIsSuccess(result) {
		_ = model.DeleteVolcAssetByToken(c.GetInt("token_id"), req.Id)
	}
	writeVolcRaw(c, result)
}

// ============================
// 辅助：归属校验与过滤
// ============================

// volcAssetEnsureGroupOwned 校验请求中的 Group（Id 或 GroupId）归属当前令牌。
func volcAssetEnsureGroupOwned(c *gin.Context, groupId, idField string) bool {
	target := idField
	if target == "" {
		target = groupId
	}
	if target == "" {
		volcAssetError(c, http.StatusBadRequest, "asset group id is required")
		return false
	}
	owned, err := model.IsVolcAssetGroupOwnedByToken(c.GetInt("token_id"), target)
	if err != nil {
		volcAssetError(c, http.StatusInternalServerError, err.Error())
		return false
	}
	if !owned {
		volcAssetError(c, http.StatusForbidden, "asset group does not belong to current token")
		return false
	}
	return true
}

// filterVolcItemsByOwnership 解析火山 List 响应，仅保留归属当前令牌的 Items，
// 并修正 TotalCount。isAsset=true 过滤 Asset，否则过滤 Asset Group。
// 解析失败时返回 nil，调用方应回退为透传原始响应。
func filterVolcItemsByOwnership(c *gin.Context, body []byte, isAsset bool) []byte {
	var raw map[string]any
	if err := common.Unmarshal(body, &raw); err != nil {
		return nil
	}
	resultRaw, ok := raw["Result"].(map[string]any)
	if !ok {
		return nil
	}
	itemsRaw, ok := resultRaw["Items"].([]any)
	if !ok {
		// 无 Items 字段，无需过滤。
		return nil
	}

	tokenId := c.GetInt("token_id")
	ownedIds := make(map[string]bool)
	if isAsset {
		assets, err := model.ListVolcAssetsByToken(tokenId, "")
		if err != nil {
			return nil
		}
		for _, a := range assets {
			ownedIds[a.VolcAssetId] = true
		}
	} else {
		groups, err := model.ListVolcAssetGroupsByToken(tokenId)
		if err != nil {
			return nil
		}
		for _, g := range groups {
			ownedIds[g.VolcGroupId] = true
		}
	}

	filtered := make([]any, 0, len(itemsRaw))
	for _, item := range itemsRaw {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := itemMap["Id"].(string)
		if ownedIds[id] {
			filtered = append(filtered, item)
		}
	}

	resultRaw["Items"] = filtered
	resultRaw["TotalCount"] = len(filtered)
	raw["Result"] = resultRaw

	out, err := common.Marshal(raw)
	if err != nil {
		return nil
	}
	return out
}
