package model

// TokenHub portrait records are separate from the AK/SK-based VolcAsset tables.
// Scope is a digest of user, token, channel, upstream credential and project.
// No upstream credentials, liveness tokens or signed source URLs are persisted.
type TokenHubPortraitSession struct {
	TokenHash string `gorm:"primaryKey;type:varchar(64)"`
	Scope     string `gorm:"index;type:varchar(64);not null"`
	ExpiresAt int64
	GroupId   string `gorm:"type:varchar(128)"`
}

type TokenHubPortraitGroup struct {
	BindingKey string `gorm:"primaryKey;type:varchar(64)"`
	Scope      string `gorm:"index;type:varchar(64);not null"`
	GroupId    string `gorm:"type:varchar(128);not null"`
	GroupType  string `gorm:"type:varchar(32);not null"`
	Name       string `gorm:"type:varchar(255)"`
	Revoked    bool   `gorm:"not null;default:false"`
}

type TokenHubPortraitAsset struct {
	CacheKey    string `gorm:"primaryKey;type:varchar(64)"`
	Scope       string `gorm:"index;type:varchar(64);not null"`
	GroupId     string `gorm:"type:varchar(128);not null"`
	AssetId     string `gorm:"index;type:varchar(128)"`
	AssetType   string `gorm:"type:varchar(16);not null"`
	Status      string `gorm:"type:varchar(32);not null"`
	Name        string `gorm:"type:varchar(80);not null"`
	UpdatedAt   int64
	FailureCode string `gorm:"type:varchar(160)"`
}
