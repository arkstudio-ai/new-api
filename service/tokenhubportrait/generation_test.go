package tokenhubportrait

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestGenerationErrorRecognizesWrappedPrivacyRejection(t *testing.T) {
	e := GenerationError([]byte(`{"code":"fail_to_fetch_task","message":"{\"error\":{\"code\":\"InputVideoSensitiveContentDetected.PrivacyInformation\",\"message\":\"signed private URL\"}}"}`))
	require.NotNil(t, e)
	require.Equal(t, "portrait_input_privacy_rejected", e.Code)
	require.NotContains(t, e.Message, "signed private URL")
	require.Nil(t, GenerationError([]byte(`{"error":{"code":"unknown","message":"secret"}}`)))
}

func TestGenerationErrorRecognizesActiveAssetMissingAtInference(t *testing.T) {
	inner, err := common.Marshal(map[string]any{"error": map[string]any{"code": "InvalidParameter", "message": "The parameter `content[0].image_url.url` specified in the request is not valid: The specified asset asset-20261016182611-zhsq6 is not found. Request id: private-request-id"}})
	require.NoError(t, err)
	raw, err := common.Marshal(map[string]any{"code": "fail_to_fetch_task", "message": string(inner)})
	require.NoError(t, err)
	e := GenerationError(raw)
	require.NotNil(t, e)
	require.Equal(t, "portrait_asset_not_found", e.Code)
	require.NotContains(t, e.Message, "private-request-id")
	require.NotContains(t, e.Message, "asset-20261016182611-zhsq6")
	require.Nil(t, GenerationError([]byte(`{"error":{"code":"InvalidParameter","message":"Duration is not valid"}}`)))
}
