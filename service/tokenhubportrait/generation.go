package tokenhubportrait

import (
	"regexp"

	"github.com/QuantumNous/new-api/common"
)

var missingAssetMessage = regexp.MustCompile(`The specified asset asset-[A-Za-z0-9-]+ is not found(?:[.\s]|$)`)

// GenerationError unwraps TokenHub's JSON-inside-message envelope. Only known
// error codes are exposed; arbitrary upstream text can contain signed URLs.
func GenerationError(raw []byte) *Error {
	type upstreamError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	var outer struct {
		Code    string        `json:"code"`
		Message string        `json:"message"`
		Error   upstreamError `json:"error"`
	}
	if len(raw) > 65536 || common.Unmarshal(raw, &outer) != nil {
		return nil
	}
	detail := outer.Error
	if detail.Code == "" && len(outer.Message) <= 32768 {
		var inner struct {
			Error upstreamError `json:"error"`
		}
		if common.UnmarshalJsonStr(outer.Message, &inner) == nil {
			detail = inner.Error
		}
	}
	if detail.Code == "InvalidParameter" && missingAssetMessage.MatchString(detail.Message) {
		detail.Code = "AssetNotFound"
	}
	switch detail.Code {
	case "InputVideoSensitiveContentDetected.PrivacyInformation", "InputImageSensitiveContentDetected.PrivacyInformation":
		return Fail("portrait_input_privacy_rejected", "参考素材触发真人隐私审核；当前流程仅支持虚拟人像，请使用可入库的虚拟素材，待 Active 后通过 asset://素材ID 生成。", 400)
	case "AssetNotFound", "InvalidAssetId", "InvalidParameter.AssetId":
		return Fail("portrait_asset_not_found", "生成端无法读取引用素材，请核对 TokenHub 素材库与视频通道的上游账户、项目及素材绑定。", 400)
	}
	return nil
}
