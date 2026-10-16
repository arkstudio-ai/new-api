package tokenhubportrait

import "github.com/QuantumNous/new-api/common"

// GenerationError unwraps TokenHub's JSON-inside-message envelope. Only known
// error codes are exposed; arbitrary upstream text can contain signed URLs.
func GenerationError(raw []byte) *Error {
	type upstreamError struct {
		Code string `json:"code"`
	}
	var outer struct {
		Code    string        `json:"code"`
		Message string        `json:"message"`
		Error   upstreamError `json:"error"`
	}
	if len(raw) > 65536 || common.Unmarshal(raw, &outer) != nil {
		return nil
	}
	code := outer.Error.Code
	if code == "" && len(outer.Message) <= 32768 {
		var inner struct {
			Error upstreamError `json:"error"`
		}
		if common.UnmarshalJsonStr(outer.Message, &inner) == nil {
			code = inner.Error.Code
		}
	}
	switch code {
	case "InputVideoSensitiveContentDetected.PrivacyInformation", "InputImageSensitiveContentDetected.PrivacyInformation":
		return Fail("portrait_input_privacy_rejected", "参考素材触发真人隐私审核；请完成本人认证、将素材入库至 Active，并使用 asset://素材ID 生成。", 400)
	case "AssetNotFound", "InvalidAssetId", "InvalidParameter.AssetId":
		return Fail("portrait_asset_not_found", "生成引用的素材不存在或不属于当前上游账户，请重新核对素材绑定。", 400)
	}
	return nil
}
