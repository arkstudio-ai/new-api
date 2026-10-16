package sora

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTokenHubEarlyUnknownAndResultURL(t *testing.T) {
	a := &TaskAdaptor{}
	a.Init(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelOtherSettings: dto.ChannelOtherSettings{TokenHubPortraitEnabled: true}}})
	for _, raw := range []string{`{"status":"unknown"}`, `{"status":"in_progress"}`} {
		result, err := a.ParseTaskResult([]byte(raw))
		require.NoError(t, err)
		require.Equal(t, model.TaskStatusInProgress, result.Status)
	}
	result, err := a.ParseTaskResult([]byte(`{"status":"completed","metadata":{"url":"https://media.example.com/final.mp4"}}`))
	require.NoError(t, err)
	require.Equal(t, model.TaskStatusSuccess, result.Status)
	require.Equal(t, "https://media.example.com/final.mp4", result.Url)
	_, err = a.ParseTaskResult([]byte(`{"status":"completed"}`))
	require.Error(t, err)
	// Generic Sora still uses the existing /content endpoint and status policy.
	generic := &TaskAdaptor{}
	result, err = generic.ParseTaskResult([]byte(`{"status":"completed"}`))
	require.NoError(t, err)
	require.Empty(t, result.Url)
	result, err = generic.ParseTaskResult([]byte(`{"status":"unknown"}`))
	require.NoError(t, err)
	require.Empty(t, result.Status)
}

func TestTokenHubPreparedWireBodyPreservesExplicitZeroAndFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	original := `{"model":"public-model","prompt":"tea","metadata":{"duration":15,"video_url":"https://original.example.com/a"}}`
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(original))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("tokenhub_prepared_body", map[string]any{"model": "public-model", "prompt": "tea", "metadata": map[string]any{"generate_audio": false, "seed": 0, "duration": 15, "content": []any{map[string]any{"type": "video_url", "role": "reference_video", "video_url": map[string]any{"url": "asset://verified-asset"}}}}})
	a := &TaskAdaptor{}
	body, err := a.BuildRequestBody(ctx, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "doubao-seedance-2-0-260128"}})
	require.NoError(t, err)
	raw, err := io.ReadAll(body)
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, common.Unmarshal(raw, &parsed))
	require.Equal(t, "doubao-seedance-2-0-260128", parsed["model"])
	require.NotContains(t, string(raw), "original.example.com")
	metadata := parsed["metadata"].(map[string]any)
	require.Equal(t, false, metadata["generate_audio"])
	require.EqualValues(t, 0, metadata["seed"])
	require.EqualValues(t, 15, metadata["duration"])
	require.Contains(t, string(raw), "asset://verified-asset")
	storage, err := common.GetBodyStorage(ctx)
	require.NoError(t, err)
	cached, err := storage.Bytes()
	require.NoError(t, err)
	require.Equal(t, original, string(cached))
}

func TestPublicTaskIDsDoNotExposeUpstreamTaskID(t *testing.T) {
	a := &TaskAdaptor{}
	raw, err := a.ConvertToOpenAIVideo(&model.Task{TaskID: "task_public", Data: []byte(`{"id":"upstream-private","task_id":"upstream-private"}`)})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "upstream-private")
}
