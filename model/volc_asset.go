package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// VolcAssetGroup 记录火山方舟 Asset Group（素材资产组合）与 new-api 令牌的归属关系。
// 用于按令牌维度隔离素材库：每条记录绑定创建该组合的 token / user / channel。
type VolcAssetGroup struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	TokenId     int    `json:"token_id" gorm:"index;not null"`
	UserId      int    `json:"user_id" gorm:"index;not null"`
	ChannelId   int    `json:"channel_id" gorm:"not null"`
	VolcGroupId string `json:"volc_group_id" gorm:"type:varchar(128);index;not null"` // 火山返回的 Asset Group Id
	Name        string `json:"name" gorm:"type:varchar(255)"`
	ProjectName string `json:"project_name" gorm:"type:varchar(255)"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

func (VolcAssetGroup) TableName() string {
	return "volc_asset_groups"
}

// VolcAsset 记录火山方舟 Asset（素材资产）与 new-api 令牌的归属关系。
type VolcAsset struct {
	Id          int    `json:"id" gorm:"primaryKey;autoIncrement"`
	TokenId     int    `json:"token_id" gorm:"index;not null"`
	UserId      int    `json:"user_id" gorm:"index;not null"`
	ChannelId   int    `json:"channel_id" gorm:"not null"`
	VolcAssetId string `json:"volc_asset_id" gorm:"type:varchar(128);index;not null"` // 火山返回的 Asset Id
	VolcGroupId string `json:"volc_group_id" gorm:"type:varchar(128);index"`
	Name        string `json:"name" gorm:"type:varchar(255)"`
	AssetType   string `json:"asset_type" gorm:"type:varchar(32)"`
	Status      string `json:"status" gorm:"type:varchar(32)"`
	ProjectName string `json:"project_name" gorm:"type:varchar(255)"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

func (VolcAsset) TableName() string {
	return "volc_assets"
}

// ============================
// 渠道路由：按分组选取火山渠道
// ============================

// GetVolcAssetChannelByGroup 返回指定分组下、已启用且配置了火山 Asset AK/SK 的
// VolcEngine 渠道，按优先级（priority 降序）取第一个。Asset 接口无 model 字段，
// 故不走标准 Distribute 的按-model 选渠道逻辑。
func GetVolcAssetChannelByGroup(group string) (*Channel, error) {
	if strings.TrimSpace(group) == "" {
		return nil, fmt.Errorf("token group is empty")
	}
	var channels []*Channel
	// 火山系渠道：方舟/豆包通用(45) 与 豆包视频(54) 都走火山 OpenAPI（同一 ark 端点、
	// 同一套 AK/SK），Asset 素材库 AK/SK 可挂在其中任一渠道上。
	query := DB.Where("type IN ? AND status = ?",
		[]int{constant.ChannelTypeVolcEngine, constant.ChannelTypeDoubaoVideo},
		common.ChannelStatusEnabled)
	query = ApplyChannelGroupFilter(query, group)
	if err := query.Order("priority DESC").Find(&channels).Error; err != nil {
		return nil, err
	}
	for _, ch := range channels {
		settings := ch.GetOtherSettings()
		if strings.TrimSpace(settings.VolcAccessKey) == "" ||
			strings.TrimSpace(settings.VolcSecretKey) == "" {
			continue
		}
		return ch, nil
	}
	return nil, fmt.Errorf("no enabled volcengine channel with asset AK/SK configured for group: %s", group)
}

// ============================
// Asset Group 归属表操作
// ============================

// RecordVolcAssetGroup 持久化一条 Asset Group 归属记录。
func RecordVolcAssetGroup(tokenId, userId, channelId int, volcGroupId, name, projectName string) error {
	now := time.Now().Unix()
	group := &VolcAssetGroup{
		TokenId:     tokenId,
		UserId:      userId,
		ChannelId:   channelId,
		VolcGroupId: volcGroupId,
		Name:        name,
		ProjectName: projectName,
		CreatedTime: now,
		UpdatedTime: now,
	}
	return DB.Create(group).Error
}

// ListVolcAssetGroupsByToken 返回指定令牌创建的所有 Asset Group。
func ListVolcAssetGroupsByToken(tokenId int) ([]VolcAssetGroup, error) {
	var groups []VolcAssetGroup
	err := DB.Where("token_id = ?", tokenId).Order("id DESC").Find(&groups).Error
	return groups, err
}

// IsVolcAssetGroupOwnedByToken 校验某个火山 Asset Group 是否归属指定令牌。
func IsVolcAssetGroupOwnedByToken(tokenId int, volcGroupId string) (bool, error) {
	var count int64
	err := DB.Model(&VolcAssetGroup{}).
		Where("token_id = ? AND volc_group_id = ?", tokenId, volcGroupId).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// UpdateVolcAssetGroupMeta 更新本地记录的名称等元信息（归属不变）。
func UpdateVolcAssetGroupMeta(tokenId int, volcGroupId, name string) error {
	updates := map[string]any{
		"updated_time": time.Now().Unix(),
	}
	if name != "" {
		updates["name"] = name
	}
	return DB.Model(&VolcAssetGroup{}).
		Where("token_id = ? AND volc_group_id = ?", tokenId, volcGroupId).
		Updates(updates).Error
}

// ============================
// Asset 归属表操作
// ============================

// RecordVolcAsset 持久化一条 Asset 归属记录。
func RecordVolcAsset(tokenId, userId, channelId int, volcAssetId, volcGroupId, name, assetType, projectName string) error {
	now := time.Now().Unix()
	asset := &VolcAsset{
		TokenId:     tokenId,
		UserId:      userId,
		ChannelId:   channelId,
		VolcAssetId: volcAssetId,
		VolcGroupId: volcGroupId,
		Name:        name,
		AssetType:   assetType,
		ProjectName: projectName,
		CreatedTime: now,
		UpdatedTime: now,
	}
	return DB.Create(asset).Error
}

// ListVolcAssetsByToken 返回指定令牌创建的 Asset，可选按 Group 过滤。
func ListVolcAssetsByToken(tokenId int, volcGroupId string) ([]VolcAsset, error) {
	var assets []VolcAsset
	query := DB.Where("token_id = ?", tokenId)
	if volcGroupId != "" {
		query = query.Where("volc_group_id = ?", volcGroupId)
	}
	err := query.Order("id DESC").Find(&assets).Error
	return assets, err
}

// IsVolcAssetOwnedByToken 校验某个火山 Asset 是否归属指定令牌。
func IsVolcAssetOwnedByToken(tokenId int, volcAssetId string) (bool, error) {
	var count int64
	err := DB.Model(&VolcAsset{}).
		Where("token_id = ? AND volc_asset_id = ?", tokenId, volcAssetId).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// DeleteVolcAssetByToken 删除指定令牌下某个火山 Asset 的本地归属记录。
// 上游删除成功后调用，保持本地归属表与火山侧一致。
func DeleteVolcAssetByToken(tokenId int, volcAssetId string) error {
	return DB.Where("token_id = ? AND volc_asset_id = ?", tokenId, volcAssetId).
		Delete(&VolcAsset{}).Error
}

// UpdateVolcAssetMeta 更新本地 Asset 记录的名称 / 状态等信息（归属不变）。
func UpdateVolcAssetMeta(tokenId int, volcAssetId string, name, status string) error {
	updates := map[string]any{
		"updated_time": time.Now().Unix(),
	}
	if name != "" {
		updates["name"] = name
	}
	if status != "" {
		updates["status"] = status
	}
	return DB.Model(&VolcAsset{}).
		Where("token_id = ? AND volc_asset_id = ?", tokenId, volcAssetId).
		Updates(updates).Error
}
