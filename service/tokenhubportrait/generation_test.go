package tokenhubportrait

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGenerationErrorRecognizesWrappedPrivacyRejection(t *testing.T) {
	e := GenerationError([]byte(`{"code":"fail_to_fetch_task","message":"{\"error\":{\"code\":\"InputVideoSensitiveContentDetected.PrivacyInformation\",\"message\":\"signed private URL\"}}"}`))
	require.NotNil(t, e)
	require.Equal(t, "portrait_input_privacy_rejected", e.Code)
	require.NotContains(t, e.Message, "signed private URL")
	require.Nil(t, GenerationError([]byte(`{"error":{"code":"unknown","message":"secret"}}`)))
}
