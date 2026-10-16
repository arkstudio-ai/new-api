package tokenhubportrait

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (c *Client) CreateSession(ctx context.Context, callback string) (map[string]any, error) {
	body := map[string]any{}
	// Callers may omit callbacks and poll using the token. Callback payloads are
	// not accepted as proof of authentication; only validate-result is trusted.
	if callback != "" {
		body["CallbackURL"] = callback
	}
	result, err := c.Call(ctx, http.MethodPost, "session", body)
	if err != nil {
		return nil, err
	}
	token, link := String(result, "BytedToken"), String(result, "H5Link")
	u, e := url.Parse(link)
	if token == "" || e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, Fail("portrait_upstream_invalid_response", "认证服务未返回有效的认证链接。", 502)
	}
	row := model.TokenHubPortraitSession{TokenHash: Digest(c.Scope + "\n" + token), Scope: c.Scope, ExpiresAt: time.Now().Unix() + 120}
	if err = model.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) ValidateSession(ctx context.Context, token string) (map[string]any, error) {
	var row model.TokenHubPortraitSession
	if token == "" {
		return nil, Fail("portrait_session_required", "需要提供本次认证凭证。", 400)
	}
	if err := model.DB.WithContext(ctx).Where("token_hash = ? AND scope = ?", Digest(c.Scope+"\n"+token), c.Scope).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, Fail("portrait_session_not_found", "认证会话不属于当前调用者或上游账户。", 404)
		}
		return nil, err
	}
	if row.GroupId != "" {
		if _, err := c.OwnGroup(ctx, row.GroupId); err != nil {
			return nil, err
		}
		return map[string]any{"GroupId": row.GroupId}, nil
	}
	if row.ExpiresAt <= time.Now().Unix() {
		return nil, Fail("portrait_session_expired", "认证会话已过期，请重新打开认证链接。", 410)
	}
	result, err := c.Call(ctx, http.MethodPost, "validate-result", map[string]any{"BytedToken": token})
	if err != nil {
		if PublicError(err).Code == "portrait_upstream_NotFound" {
			return nil, Fail("portrait_authentication_pending", "等待本人完成手机端活体认证。", 409)
		}
		return nil, err
	}
	id := String(result, "GroupId")
	if id == "" {
		return nil, Fail("portrait_upstream_invalid_response", "认证服务未返回资产组。", 502)
	}
	group, err := c.Call(ctx, http.MethodGet, "asset-group/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if String(group, "Id") != id || String(group, "GroupType") != "LivenessFace" || !strings.EqualFold(String(group, "Status"), "active") {
		return nil, Fail("portrait_group_not_active", "真人认证资产组尚未就绪。", 409)
	}
	err = model.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding := model.TokenHubPortraitGroup{BindingKey: Digest(c.Scope + "\n" + id), Scope: c.Scope, GroupId: id, GroupType: "LivenessFace", Name: String(group, "Name")}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; e != nil {
			return e
		}
		return tx.Model(&row).Update("group_id", id).Error
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) CreateGroup(ctx context.Context, name string) (map[string]any, error) {
	if strings.TrimSpace(name) == "" || len(name) > 255 {
		return nil, Fail("portrait_group_name_invalid", "需要提供不超过 255 字节的虚拟素材组名称。", 400)
	}
	result, err := c.Call(ctx, http.MethodPost, "asset-group", map[string]any{"Name": name, "GroupType": "AIGC"})
	if err != nil {
		return nil, err
	}
	id := String(result, "Id")
	if id == "" {
		return nil, Fail("portrait_upstream_invalid_response", "素材库未返回资产组 ID。", 502)
	}
	row := model.TokenHubPortraitGroup{BindingKey: Digest(c.Scope + "\n" + id), Scope: c.Scope, GroupId: id, GroupType: "AIGC", Name: name}
	if err = model.DB.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) OwnGroup(ctx context.Context, id string) (*model.TokenHubPortraitGroup, error) {
	if id == "" {
		return nil, Fail("portrait_authentication_required", "请先完成真人认证，并在 metadata.portrait_group_id 指定所属资产组；虚拟素材请先创建 AIGC 组。", 409)
	}
	var row model.TokenHubPortraitGroup
	err := model.DB.WithContext(ctx).Where("scope = ? AND group_id = ? AND revoked = ?", c.Scope, id, false).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, Fail("portrait_group_not_found", "资产组不属于当前调用者或上游账户。", 404)
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (c *Client) Group(ctx context.Context, id string) (map[string]any, error) {
	row, err := c.OwnGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	result, err := c.Call(ctx, http.MethodGet, "asset-group/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	if String(result, "Id") != id || String(result, "GroupType") != row.GroupType || !strings.EqualFold(String(result, "Status"), "active") {
		return nil, Fail("portrait_group_not_active", "素材资产组已失效或尚未就绪。", 409)
	}
	return result, nil
}

func (c *Client) ListGroups(ctx context.Context) ([]model.TokenHubPortraitGroup, error) {
	var rows []model.TokenHubPortraitGroup
	err := model.DB.WithContext(ctx).Where("scope = ? AND revoked = ?", c.Scope, false).Find(&rows).Error
	return rows, err
}

func (c *Client) ListAssets(ctx context.Context, group string) ([]model.TokenHubPortraitAsset, error) {
	if _, err := c.OwnGroup(ctx, group); err != nil {
		return nil, err
	}
	var rows []model.TokenHubPortraitAsset
	err := model.DB.WithContext(ctx).Where("scope = ? AND group_id = ?", c.Scope, group).Find(&rows).Error
	return rows, err
}

func (c *Client) Asset(ctx context.Context, id string) (map[string]any, error) {
	var row model.TokenHubPortraitAsset
	err := model.DB.WithContext(ctx).Where("scope = ? AND asset_id = ?", c.Scope, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || id == "" {
		return nil, Fail("portrait_asset_not_found", "素材不属于当前调用者或上游账户。", 404)
	}
	if err != nil {
		return nil, err
	}
	return c.refreshAsset(ctx, &row)
}

func (c *Client) refreshAsset(ctx context.Context, row *model.TokenHubPortraitAsset) (map[string]any, error) {
	if row.Status == "Deleted" {
		return nil, Fail("portrait_asset_not_found", "素材已被删除。", 404)
	}
	if _, err := c.OwnGroup(ctx, row.GroupId); err != nil {
		return nil, err
	}
	if row.AssetId == "" {
		return nil, Fail("portrait_asset_submission_unknown", "素材入库结果未确认，请先查询恢复状态。", 409)
	}
	result, err := c.Call(ctx, http.MethodGet, "asset/"+url.PathEscape(row.AssetId), nil)
	if err != nil {
		return nil, err
	}
	if String(result, "Id") != row.AssetId || String(result, "GroupId") != row.GroupId || String(result, "AssetType") != row.AssetType {
		return nil, Fail("portrait_asset_identity_mismatch", "素材库返回的素材身份或归属不一致。", 502)
	}
	status := String(result, "Status")
	if status != "Active" && status != "Processing" && status != "Failed" {
		return nil, Fail("portrait_asset_status_unknown", "素材状态尚未确认，暂停生成。", 409)
	}
	if err = model.DB.WithContext(ctx).Model(row).Updates(map[string]any{"status": status, "updated_at": time.Now().Unix()}).Error; err != nil {
		return nil, err
	}
	row.Status = status
	return result, nil
}

// EnsureAsset reserves a unique content digest BEFORE creating the upstream
// asset. Unknown create outcomes are reconciled by deterministic Name, never
// retried as new uploads. This also works across processes and after restarts.
func (c *Client) EnsureAsset(ctx context.Context, group, source, kind string, wait time.Duration) (*model.TokenHubPortraitAsset, error) {
	if kind != "Image" && kind != "Video" && kind != "Audio" {
		return nil, Fail("portrait_asset_type_invalid", "素材类型仅支持 Image、Video、Audio。", 400)
	}
	if _, err := c.Group(ctx, group); err != nil {
		return nil, err
	}
	var row model.TokenHubPortraitAsset
	if strings.HasPrefix(source, "asset://") {
		id := strings.TrimPrefix(source, "asset://")
		err := model.DB.WithContext(ctx).Where("scope = ? AND group_id = ? AND asset_id = ? AND asset_type = ?", c.Scope, group, id, kind).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, Fail("portrait_asset_not_found", "素材 URI 不属于当前资产组或素材类型不匹配。", 404)
		}
		if err != nil {
			return nil, err
		}
	} else {
		digest, err := c.SourceDigest(ctx, source)
		if err != nil {
			return nil, err
		}
		key := Digest(c.Scope + "\n" + group + "\n" + kind + "\n" + digest)
		row = model.TokenHubPortraitAsset{CacheKey: key, Scope: c.Scope, GroupId: group, AssetType: kind, Status: "Uploading", Name: "na-" + key, UpdatedAt: time.Now().Unix()}
		result := model.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 1 {
			asset, err := c.Call(ctx, http.MethodPost, "asset", map[string]any{"GroupId": group, "URL": source, "AssetType": kind, "Name": row.Name})
			if err != nil || String(asset, "Id") == "" {
				// Use a fresh context so client cancellation cannot leave a permanent
				// in-flight reservation. Even if this update fails, Uploading recovers.
				dbctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				status, code := "Unknown", ""
				if err != nil && PublicError(err).Rejected {
					status, code = "Rejected", PublicError(err).Code
				}
				updateErr := model.DB.WithContext(dbctx).Model(&row).Updates(map[string]any{"status": status, "failure_code": code, "updated_at": time.Now().Unix()}).Error
				cancel()
				if updateErr != nil {
					return nil, updateErr
				}
				if status == "Rejected" {
					return nil, err
				}
				return nil, Fail("portrait_asset_submission_unknown", "素材上传结果未确认；再次检查时将查询已有素材，避免重复上传。", 409)
			}
			row.AssetId = String(asset, "Id")
			row.Status = "Processing"
			if err = model.DB.WithContext(ctx).Model(&row).Updates(map[string]any{"asset_id": row.AssetId, "status": row.Status, "updated_at": time.Now().Unix()}).Error; err != nil {
				return nil, err
			}
		} else {
			if err = model.DB.WithContext(ctx).Where("cache_key = ?", key).First(&row).Error; err != nil {
				return nil, err
			}
			if row.Status == "Rejected" {
				return nil, Fail(row.FailureCode, "此前素材上传已被上游拒绝，请核对认证及素材后再处理。", 422)
			}
			if row.AssetId == "" {
				if row.Status == "Uploading" && row.UpdatedAt > time.Now().Unix()-60 {
					return nil, Fail("portrait_asset_processing", "同一素材正在入库，请稍后继续查询。", 409)
				}
				if err = c.reconcile(ctx, &row); err != nil {
					return nil, err
				}
			}
		}
	}
	deadline := time.Now().Add(wait)
	for {
		if _, err := c.refreshAsset(ctx, &row); err != nil {
			return nil, err
		}
		if row.Status == "Active" {
			return &row, nil
		}
		if row.Status == "Failed" {
			return nil, Fail("portrait_asset_failed", "素材库审核失败，请检查素材是否与认证人物一致。", 422)
		}
		if wait <= 0 {
			return &row, nil
		}
		if !time.Now().Before(deadline) {
			return nil, Fail("portrait_asset_processing", "素材已入库但仍在处理，请稍后继续；尚未提交视频生成。", 409)
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (c *Client) reconcile(ctx context.Context, row *model.TokenHubPortraitAsset) error {
	// Never list the upstream account globally: only this owned group is queried.
	for page := 1; page <= 20; page++ {
		result, err := c.Call(ctx, http.MethodPost, "asset/list", map[string]any{"Filter": map[string]any{"GroupIds": []string{row.GroupId}}, "PageNumber": page, "PageSize": 100})
		if err != nil {
			return err
		}
		items, ok := result["Items"].([]any)
		if result["Items"] == nil {
			items, ok = []any{}, true
		}
		if !ok {
			return Fail("portrait_upstream_invalid_response", "素材库列表响应无效。", 502)
		}
		for _, item := range items {
			asset, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if String(asset, "Name") == row.Name && String(asset, "GroupId") == row.GroupId && String(asset, "AssetType") == row.AssetType && String(asset, "Id") != "" {
				row.AssetId = String(asset, "Id")
				return model.DB.WithContext(ctx).Model(row).Updates(map[string]any{"asset_id": row.AssetId, "status": "Processing", "updated_at": time.Now().Unix()}).Error
			}
		}
		if len(items) < 100 {
			break
		}
	}
	return Fail("portrait_asset_submission_unknown", fmt.Sprintf("素材入库结果仍未确认（%s），暂停重复上传，请联系管理员核对。", row.Name), 409)
}
