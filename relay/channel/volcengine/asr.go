package volcengine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	volcengineASRSubmitURL          = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/submit"
	volcengineASRQueryURL           = "https://openspeech.bytedance.com/api/v3/auc/bigmodel/query"
	volcengineASRDefaultResourceID  = "volc.seedasr.auc"
	volcengineASRSuccessCode        = "20000000"
	volcengineASRProcessingCode     = "20000001"
	volcengineASRQueueingCode       = "20000002"
	volcengineASRSilenceCode        = "20000003"
	volcengineASRDefaultPollTimeout = 180 * time.Second
	volcengineASRPollInterval       = time.Second

	contextKeyASRRequestID      = "volcengine_asr_request_id"
	contextKeyASRResourceID     = "volcengine_asr_resource_id"
	contextKeyASRResponseFormat = "volcengine_asr_response_format"
)

type VolcengineASRRequest struct {
	User         VolcengineASRUser        `json:"user,omitempty"`
	Audio        VolcengineASRAudio       `json:"audio"`
	Request      VolcengineASRRequestInfo `json:"request"`
	Callback     string                   `json:"callback,omitempty"`
	CallbackData string                   `json:"callback_data,omitempty"`
}

type VolcengineASRUser struct {
	UID string `json:"uid,omitempty"`
}

type VolcengineASRAudio struct {
	URL      string `json:"url"`
	Format   string `json:"format"`
	Language string `json:"language,omitempty"`
	Codec    string `json:"codec,omitempty"`
	Rate     int    `json:"rate,omitempty"`
	Bits     int    `json:"bits,omitempty"`
	Channel  int    `json:"channel,omitempty"`
}

type VolcengineASRRequestInfo struct {
	ModelName              string `json:"model_name"`
	SSDVersion             string `json:"ssd_version,omitempty"`
	EnableITN              *bool  `json:"enable_itn,omitempty"`
	EnablePunc             *bool  `json:"enable_punc,omitempty"`
	EnableDDC              *bool  `json:"enable_ddc,omitempty"`
	EnableSpeakerInfo      *bool  `json:"enable_speaker_info,omitempty"`
	EnableChannelSplit     *bool  `json:"enable_channel_split,omitempty"`
	ShowUtterances         *bool  `json:"show_utterances,omitempty"`
	ShowSpeechRate         *bool  `json:"show_speech_rate,omitempty"`
	ShowVolume             *bool  `json:"show_volume,omitempty"`
	EnableAutoLang         *bool  `json:"enable_auto_lang,omitempty"`
	EnableLID              *bool  `json:"enable_lid,omitempty"`
	EnableEmotionDetection *bool  `json:"enable_emotion_detection,omitempty"`
}

type volcengineASRQueryResponse struct {
	AudioInfo *struct {
		Duration int `json:"duration"`
	} `json:"audio_info,omitempty"`
	Result *volcengineASRResult `json:"result,omitempty"`
}

type volcengineASRResult struct {
	Text      string                   `json:"text,omitempty"`
	Utterance []volcengineASRUtterance `json:"utterances,omitempty"`
	Additions map[string]any           `json:"additions,omitempty"`
}

type volcengineASRUtterance struct {
	Text      string `json:"text,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

func buildVolcengineASRRequest(c *gin.Context, request dto.AudioRequest) (*VolcengineASRRequest, string, error) {
	audioURL := strings.TrimSpace(request.AudioURL)
	if audioURL == "" {
		audioURL = strings.TrimSpace(request.URL)
	}
	if audioURL == "" {
		return nil, "", errors.New("audio_url is required for volcengine Seed-ASR")
	}

	format := inferAudioFormat(audioURL)
	language := rawMessageString(request.Language)
	showUtterances := request.ResponseFormat == "verbose_json"
	enableITN := true
	enablePunc := true

	asrRequest := &VolcengineASRRequest{
		User: VolcengineASRUser{
			UID: "openai_relay_user",
		},
		Audio: VolcengineASRAudio{
			URL:      audioURL,
			Format:   format,
			Language: language,
		},
		Request: VolcengineASRRequestInfo{
			ModelName:      "bigmodel",
			EnableITN:      &enableITN,
			EnablePunc:     &enablePunc,
			ShowUtterances: &showUtterances,
		},
	}

	resourceID := volcengineASRDefaultResourceID
	if len(request.Metadata) > 0 {
		var metadata struct {
			ResourceID   string                    `json:"resource_id,omitempty"`
			User         *VolcengineASRUser        `json:"user,omitempty"`
			Audio        *VolcengineASRAudio       `json:"audio,omitempty"`
			Request      *VolcengineASRRequestInfo `json:"request,omitempty"`
			Callback     string                    `json:"callback,omitempty"`
			CallbackData string                    `json:"callback_data,omitempty"`
		}
		if err := common.Unmarshal(request.Metadata, &metadata); err != nil {
			return nil, "", fmt.Errorf("error unmarshalling metadata to volcengine ASR request: %w", err)
		}
		if metadata.ResourceID != "" {
			resourceID = metadata.ResourceID
		}
		if metadata.User != nil {
			asrRequest.User = *metadata.User
		}
		if metadata.Audio != nil {
			if metadata.Audio.URL == "" {
				metadata.Audio.URL = asrRequest.Audio.URL
			}
			if metadata.Audio.Format == "" {
				metadata.Audio.Format = asrRequest.Audio.Format
			}
			if metadata.Audio.Language == "" {
				metadata.Audio.Language = asrRequest.Audio.Language
			}
			asrRequest.Audio = *metadata.Audio
		}
		if metadata.Request != nil {
			if metadata.Request.ModelName == "" {
				metadata.Request.ModelName = "bigmodel"
			}
			asrRequest.Request = *metadata.Request
		}
		asrRequest.Callback = metadata.Callback
		asrRequest.CallbackData = metadata.CallbackData
	}

	if asrRequest.Audio.Format == "" {
		asrRequest.Audio.Format = "mp3"
	}
	if asrRequest.Request.ModelName == "" {
		asrRequest.Request.ModelName = "bigmodel"
	}

	c.Set(contextKeyASRResourceID, resourceID)
	return asrRequest, resourceID, nil
}

func setupVolcengineASRHeader(header *http.Header, apiKey, requestID, resourceID string, includeSequence bool) error {
	parts := strings.Split(apiKey, "|")
	switch len(parts) {
	case 1:
		header.Set("X-Api-Key", apiKey)
	case 2, 3:
		header.Set("X-Api-App-Key", parts[0])
		header.Set("X-Api-Access-Key", parts[1])
		if len(parts) == 3 && parts[2] != "" {
			resourceID = parts[2]
		}
	default:
		return errors.New("invalid api key format, expected: api_key or appid|access_token or appid|access_token|resource_id")
	}
	header.Set("Content-Type", "application/json")
	header.Set("X-Api-Resource-Id", resourceID)
	header.Set("X-Api-Request-Id", requestID)
	if includeSequence {
		header.Set("X-Api-Sequence", "-1")
	}
	return nil
}

func handleASRResponse(c *gin.Context, submitResp *http.Response, info *relaycommon.RelayInfo) (any, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(submitResp)

	if err := checkVolcengineASRStatus(submitResp); err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway)
	}

	requestID := c.GetString(contextKeyASRRequestID)
	resourceID := c.GetString(contextKeyASRResourceID)
	result, err := pollVolcengineASRResult(c, info, requestID, resourceID)
	if err != nil {
		return nil, err
	}

	writeOpenAIASRResponse(c, result, c.GetString(contextKeyASRResponseFormat))

	usage := &dto.Usage{
		PromptTokens:     info.GetEstimatePromptTokens(),
		CompletionTokens: 0,
		TotalTokens:      info.GetEstimatePromptTokens(),
	}
	return usage, nil
}

func pollVolcengineASRResult(c *gin.Context, info *relaycommon.RelayInfo, requestID, resourceID string) (*volcengineASRQueryResponse, *types.NewAPIError) {
	deadline := time.NewTimer(volcengineASRDefaultPollTimeout)
	defer deadline.Stop()

	ticker := time.NewTicker(volcengineASRPollInterval)
	defer ticker.Stop()

	for {
		result, done, err := queryVolcengineASRResult(c, info, requestID, resourceID)
		if err != nil {
			return nil, err
		}
		if done {
			return result, nil
		}

		select {
		case <-c.Request.Context().Done():
			return nil, types.NewErrorWithStatusCode(c.Request.Context().Err(), types.ErrorCodeDoRequestFailed, http.StatusRequestTimeout)
		case <-deadline.C:
			return nil, types.NewErrorWithStatusCode(errors.New("volcengine Seed-ASR query timeout"), types.ErrorCodeBadResponse, http.StatusGatewayTimeout)
		case <-ticker.C:
		}
	}
}

func queryVolcengineASRResult(c *gin.Context, info *relaycommon.RelayInfo, requestID, resourceID string) (*volcengineASRQueryResponse, bool, *types.NewAPIError) {
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, volcengineASRQueryURL, bytes.NewReader([]byte("{}")))
	if err != nil {
		return nil, false, types.NewErrorWithStatusCode(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	if err := setupVolcengineASRHeader(&req.Header, info.ApiKey, requestID, resourceID, false); err != nil {
		return nil, false, types.NewErrorWithStatusCode(err, types.ErrorCodeChannelInvalidKey, http.StatusUnauthorized)
	}

	client := service.GetHttpClient()
	resp, err := client.Do(req)
	if err != nil {
		return nil, false, types.NewErrorWithStatusCode(err, types.ErrorCodeDoRequestFailed, http.StatusBadGateway)
	}
	defer service.CloseResponseBodyGracefully(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, types.NewErrorWithStatusCode(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}

	statusCode := resp.Header.Get("X-Api-Status-Code")
	message := resp.Header.Get("X-Api-Message")
	if resp.StatusCode != http.StatusOK {
		return nil, false, types.NewErrorWithStatusCode(fmt.Errorf("volcengine Seed-ASR query failed: status=%d code=%s message=%s", resp.StatusCode, statusCode, message), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)
	}

	var result volcengineASRQueryResponse
	if len(bytes.TrimSpace(body)) > 0 {
		if err := common.Unmarshal(body, &result); err != nil {
			return nil, false, types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
	}
	if result.Result != nil && (result.Result.Text != "" || len(result.Result.Utterance) > 0) {
		return &result, true, nil
	}
	if statusCode == volcengineASRSilenceCode {
		return &result, true, nil
	}
	if statusCode == volcengineASRProcessingCode || statusCode == volcengineASRQueueingCode {
		return &result, false, nil
	}
	if statusCode == "" || statusCode == volcengineASRSuccessCode || strings.EqualFold(message, "OK") || isVolcengineASRProcessing(message) {
		return &result, false, nil
	}
	return nil, false, types.NewErrorWithStatusCode(fmt.Errorf("volcengine Seed-ASR query failed: code=%s message=%s", statusCode, message), types.ErrorCodeBadResponse, http.StatusBadGateway)
}

func checkVolcengineASRStatus(resp *http.Response) error {
	statusCode := resp.Header.Get("X-Api-Status-Code")
	message := resp.Header.Get("X-Api-Message")
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("volcengine Seed-ASR submit failed: status=%d code=%s message=%s", resp.StatusCode, statusCode, message)
	}
	if statusCode != "" && statusCode != volcengineASRSuccessCode {
		return fmt.Errorf("volcengine Seed-ASR submit failed: code=%s message=%s", statusCode, message)
	}
	return nil
}

func writeOpenAIASRResponse(c *gin.Context, result *volcengineASRQueryResponse, responseFormat string) {
	text := ""
	if result != nil && result.Result != nil {
		text = result.Result.Text
	}
	switch responseFormat {
	case "text", "srt", "vtt":
		c.String(http.StatusOK, text)
	case "verbose_json":
		c.JSON(http.StatusOK, dto.WhisperVerboseJSONResponse{
			Text:     text,
			Duration: volcengineDurationSeconds(result),
			Segments: volcengineSegments(result),
		})
	default:
		c.JSON(http.StatusOK, dto.AudioResponse{Text: text})
	}
}

func volcengineSegments(result *volcengineASRQueryResponse) []dto.Segment {
	if result == nil || result.Result == nil {
		return nil
	}
	segments := make([]dto.Segment, 0, len(result.Result.Utterance))
	for i, utterance := range result.Result.Utterance {
		segments = append(segments, dto.Segment{
			Id:    i,
			Start: float64(utterance.StartTime) / 1000,
			End:   float64(utterance.EndTime) / 1000,
			Text:  utterance.Text,
		})
	}
	return segments
}

func volcengineDurationSeconds(result *volcengineASRQueryResponse) float64 {
	if result == nil || result.AudioInfo == nil {
		return 0
	}
	return float64(result.AudioInfo.Duration) / 1000
}

func inferAudioFormat(audioURL string) string {
	parsed, err := url.Parse(audioURL)
	path := audioURL
	if err == nil {
		path = parsed.Path
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "mp3", "wav", "ogg":
		return ext
	case "pcm", "raw":
		return "raw"
	default:
		return "mp3"
	}
}

func rawMessageString(data []byte) string {
	if len(bytes.TrimSpace(data)) == 0 {
		return ""
	}
	var value string
	if err := common.Unmarshal(data, &value); err == nil {
		return value
	}
	return strings.Trim(string(data), `"`)
}

func isVolcengineASRProcessing(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "processing") ||
		strings.Contains(lower, "running") ||
		strings.Contains(lower, "pending") ||
		strings.Contains(message, "处理中") ||
		strings.Contains(message, "排队")
}
