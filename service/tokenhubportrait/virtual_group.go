package tokenhubportrait

import (
	"context"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm/clause"
)

// DefaultVirtualGroup gives each caller/account scope its own AIGC library.
// The database reservation survives request cancellation and process restarts.
// An ambiguous upstream create is reconciled by name, never blindly repeated.
func (c *Client) DefaultVirtualGroup(ctx context.Context) (string, error) {
	key := Digest(c.Scope + "\ndefault-aigc-v1")
	row := model.TokenHubPortraitGroup{BindingKey: key, Scope: c.Scope, GroupType: "AIGC", Name: "new-api-aigc-" + c.Scope[:24], Status: "Creating", UpdatedAt: time.Now().Unix()}
	result := model.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 1 {
		group, err := c.Call(ctx, http.MethodPost, "asset-group", map[string]any{"Name": row.Name, "GroupType": "AIGC"})
		if err != nil || String(group, "Id") == "" {
			status, code := "Unknown", ""
			if err != nil && PublicError(err).Rejected {
				status, code = "Rejected", PublicError(err).Code
			}
			dbctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			updateErr := model.DB.WithContext(dbctx).Model(&row).Updates(map[string]any{"status": status, "failure_code": code, "updated_at": time.Now().Unix()}).Error
			cancel()
			if updateErr != nil {
				return "", updateErr
			}
			if status == "Rejected" {
				return "", err
			}
			return "", Fail("portrait_group_submission_unknown", "虚拟人像素材组创建结果未确认，暂停生成；后续将查询恢复已有素材组。", 409)
		}
		row.GroupId = String(group, "Id")
		if err = model.DB.WithContext(ctx).Model(&row).Updates(map[string]any{"group_id": row.GroupId, "status": "Active", "updated_at": time.Now().Unix()}).Error; err != nil {
			return "", err
		}
		return row.GroupId, nil
	}
	if err := model.DB.WithContext(ctx).Where("binding_key = ?", key).First(&row).Error; err != nil {
		return "", err
	}
	if row.Revoked {
		return "", Fail("portrait_group_not_found", "默认虚拟人像组已被删除，请明确选择新的 AIGC 组。", 404)
	}
	if row.GroupId != "" {
		return row.GroupId, nil
	}
	if row.Status == "Rejected" {
		return "", Fail(row.FailureCode, "默认虚拟人像组创建曾被拒绝，请核对素材库权益或明确选择已创建的 AIGC 组。", 409)
	}
	if row.Status == "Creating" && row.UpdatedAt > time.Now().Unix()-60 {
		return "", Fail("portrait_group_processing", "默认虚拟人像组正在创建，请稍后继续。", 409)
	}
	return c.reconcileVirtualGroup(ctx, &row)
}

func (c *Client) reconcileVirtualGroup(ctx context.Context, row *model.TokenHubPortraitGroup) (string, error) {
	for page := 1; page <= 20; page++ {
		result, err := c.Call(ctx, http.MethodPost, "asset-group/list", map[string]any{"Filter": map[string]any{"GroupType": "AIGC"}, "PageNumber": page, "PageSize": 100})
		if err != nil {
			return "", err
		}
		items, ok := result["Items"].([]any)
		if result["Items"] == nil {
			items, ok = []any{}, true
		}
		if !ok {
			return "", Fail("portrait_upstream_invalid_response", "虚拟人像组列表响应无效。", 502)
		}
		for _, item := range items {
			group, ok := item.(map[string]any)
			if !ok || String(group, "Name") != row.Name || String(group, "GroupType") != "AIGC" || String(group, "Id") == "" {
				continue
			}
			row.GroupId = String(group, "Id")
			if err = model.DB.WithContext(ctx).Model(row).Updates(map[string]any{"group_id": row.GroupId, "status": "Active", "updated_at": time.Now().Unix()}).Error; err != nil {
				return "", err
			}
			return row.GroupId, nil
		}
		if len(items) < 100 {
			break
		}
	}
	return "", Fail("portrait_group_submission_unknown", "仍未查到此前创建的虚拟人像组，暂停重复创建和视频生成，请核对上游记录。", 409)
}
