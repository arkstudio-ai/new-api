package minimaxv2

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	return fmt.Sprintf("%s%s", strings.TrimSuffix(a.baseURL, "/"), GenerateEndpoint), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return nil, fmt.Errorf("request not found in context")
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		return nil, fmt.Errorf("invalid request type in context")
	}

	body, err := a.convertToRequestPayload(&req, info)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var mResp SubmitResponse
	if err := common.Unmarshal(responseBody, &mResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if mResp.Error != nil || mResp.TaskID == "" {
		code := "minimax_v2_error"
		httpCode := resp.StatusCode
		if httpCode < http.StatusBadRequest {
			httpCode = http.StatusBadRequest
		}
		reason := "upstream returned no task_id"
		if mResp.Error != nil {
			reason = mResp.Error.String()
			if mResp.Error.Type != "" {
				code = mResp.Error.Type
			}
			if hc, convErr := strconv.Atoi(mResp.Error.HTTPCode); convErr == nil && hc >= http.StatusBadRequest {
				httpCode = hc
			}
		}
		taskErr = service.TaskErrorWrapper(fmt.Errorf("minimax v2 api error: %s", reason), code, httpCode)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return mResp.TaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || taskID == "" {
		return nil, fmt.Errorf("invalid task_id")
	}

	uri := fmt.Sprintf("%s%s/%s", strings.TrimSuffix(baseUrl, "/"), QueryEndpoint, taskID)

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*VideoRequest, error) {
	duration := DefaultDuration
	if req.Duration > 0 {
		duration = req.Duration
	} else if seconds := parseSeconds(req.Seconds); seconds > 0 {
		duration = seconds
	}

	resolution := DefaultResolution
	if req.Size != "" {
		resolution = parseResolutionFromSize(req.Size)
	}

	videoRequest := &VideoRequest{
		Model:      info.UpstreamModelName,
		Content:    buildContent(req),
		Resolution: resolution,
		Duration:   &duration,
		Ratio:      parseRatioFromSize(req.Size),
	}

	// metadata 允许整体覆盖 content / ratio / callback_url 等，便于上层直接透传官方原生请求；
	// taskcommon.UnmarshalMetadata 会剔除 model 字段，防止绕过计费。
	if err := taskcommon.UnmarshalMetadata(req.Metadata, videoRequest); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata to video request failed")
	}
	videoRequest.Model = info.UpstreamModelName

	// metadata.content 是「整块覆盖」语义(上层就是这么发多模态参考的),覆盖后
	// 由 prompt 建出来的 text 项会连带消失,而 H3 强制要求 content 含非空 text。
	// 这里把 prompt 补回数组首位,而不是甩一个 400 给调用方。
	videoRequest.Content = ensureTextContent(videoRequest.Content, req.Prompt)
	// metadata 里的 resolution 可能是上游别的厂商口径(如 sd2 的 "720p"),收敛到 H3 四档。
	videoRequest.Resolution = normalizeResolution(videoRequest.Resolution)

	if len(videoRequest.Content) == 0 {
		return nil, fmt.Errorf("content is empty: prompt is required")
	}
	if !hasText(videoRequest.Content) {
		return nil, fmt.Errorf("content must contain a non-empty text item")
	}
	if videoRequest.Duration != nil && (*videoRequest.Duration < MinDuration || *videoRequest.Duration > MaxDuration) {
		return nil, fmt.Errorf("duration must be between %d and %d seconds", MinDuration, MaxDuration)
	}

	return videoRequest, nil
}

// buildContent 把 OpenAI 风格的视频请求映射成 H3 的多模态 content 数组。
// 首图落 first_frame，其余图落 reference_image；input_reference 视作参考视频。
func buildContent(req *relaycommon.TaskSubmitReq) []ContentItem {
	content := make([]ContentItem, 0, len(req.Images)+2)
	content = append(content, ContentItem{Type: ContentTypeText, Text: req.Prompt})

	for i, img := range req.Images {
		img = strings.TrimSpace(img)
		if img == "" {
			continue
		}
		role := RoleReferenceImage
		if i == 0 {
			role = RoleFirstFrame
		}
		content = append(content, ContentItem{
			Type:     ContentTypeImageURL,
			Role:     role,
			ImageURL: &MediaURL{URL: img},
		})
	}

	if ref := strings.TrimSpace(req.InputReference); ref != "" {
		content = append(content, ContentItem{
			Type:     ContentTypeVideoURL,
			Role:     RoleReferenceVideo,
			VideoURL: &MediaURL{URL: ref},
		})
	}

	return content
}

// ensureTextContent 保证 content 里有一条非空 text —— 没有就用 prompt 补在首位。
func ensureTextContent(content []ContentItem, prompt string) []ContentItem {
	if hasText(content) {
		return content
	}
	if strings.TrimSpace(prompt) == "" {
		return content
	}
	return append([]ContentItem{{Type: ContentTypeText, Text: prompt}}, content...)
}

// normalizeResolution 把各路写法(720p / 1080P / 2k / 1920x1080…)收敛到 H3 的四档。
func normalizeResolution(res string) string {
	trimmed := strings.TrimSpace(res)
	if trimmed == "" {
		return DefaultResolution
	}
	switch strings.ToLower(trimmed) {
	case "480p":
		return Resolution480P
	case "512p":
		return Resolution512P
	case "768p", "720p":
		return Resolution768P
	case "2k", "1080p", "1440p", "2160p", "4k":
		return Resolution2K
	}
	return parseResolutionFromSize(trimmed)
}

func hasText(content []ContentItem) bool {
	for _, item := range content {
		if item.Type == ContentTypeText && strings.TrimSpace(item.Text) != "" {
			return true
		}
	}
	return false
}

// parseSeconds 兼容 OpenAI 的 "5" / "5s" 写法
func parseSeconds(seconds string) int {
	seconds = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(seconds), "s"))
	if seconds == "" {
		return 0
	}
	v, err := strconv.Atoi(seconds)
	if err != nil {
		return 0
	}
	return v
}

// parseResolutionFromSize 把 size（"1920x1080" / "2K" / "768p" 等）映射到 H3 的四个档位。
// 1080 及以上一律走 2K —— H3 没有 1080P 档，向上取是唯一能满足"高清"诉求的选择。
func parseResolutionFromSize(size string) string {
	s := strings.ToLower(size)
	switch {
	case strings.Contains(s, "2k"), strings.Contains(s, "1440"), strings.Contains(s, "2160"),
		strings.Contains(s, "4k"), strings.Contains(s, "1080"):
		return Resolution2K
	case strings.Contains(s, "768"), strings.Contains(s, "720"):
		return Resolution768P
	case strings.Contains(s, "512"):
		return Resolution512P
	case strings.Contains(s, "480"):
		return Resolution480P
	default:
		return DefaultResolution
	}
}

// parseRatioFromSize 从 "1280x720" 这类 size 推出画幅；推不出就交给上游自适应。
func parseRatioFromSize(size string) string {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(size)), "x")
	if len(parts) != 2 {
		return DefaultRatio
	}
	w, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return DefaultRatio
	}
	for _, r := range []struct {
		name  string
		ratio float64
	}{
		{"21:9", 21.0 / 9.0},
		{"16:9", 16.0 / 9.0},
		{"4:3", 4.0 / 3.0},
		{"1:1", 1.0},
		{"3:4", 3.0 / 4.0},
		{"9:16", 9.0 / 16.0},
	} {
		actual := float64(w) / float64(h)
		if actual > r.ratio*0.98 && actual < r.ratio*1.02 {
			return r.name
		}
	}
	return DefaultRatio
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var resp QueryResponse
	if err := common.Unmarshal(respBody, &resp); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{}

	// 顶层 error：查询本身失败（鉴权/限流/任务不存在）
	if resp.Task == nil {
		if resp.Error != nil {
			taskResult.Code = -1
			taskResult.Reason = resp.Error.String()
			taskResult.Status = model.TaskStatusFailure
			taskResult.Progress = "100%"
			return &taskResult, nil
		}
		return nil, errors.Errorf("unexpected task query response: %s", respBody)
	}

	task := resp.Task
	taskResult.TaskID = task.ID

	switch task.Status {
	case TaskStatusQueued:
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "20%"
	case TaskStatusRunning:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case TaskStatusSucceeded:
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		if task.Content != nil {
			taskResult.Url = task.Content.URL
		}
		// 按秒计费:把出片秒数塞进 TotalTokens,controller 完成时用
		// `TotalTokens × ModelRatio × GroupRatio` 做多退少补(与 kling 的
		// FinalUnitDeduction 同一条路)。**只有给该模型显式配了 ModelRatio 才生效**,
		// 没配就退回 ModelPrice 的按次固定价,行为与改动前一致。
		if seconds := billableSeconds(task); seconds > 0 {
			taskResult.CompletionTokens = seconds
			taskResult.TotalTokens = seconds
		}
	case TaskStatusFailed, TaskStatusCancelled:
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Code = -1
		if task.Error != nil {
			taskResult.Reason = task.Error.String()
		}
		if taskResult.Reason == "" {
			taskResult.Reason = fmt.Sprintf("task %s", task.Status)
		}
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

// AdjustBillingOnComplete 按秒结算(占位价),由轮询循环在任务成功时调用。
//
// 口径:quota = 出片秒数 × ModelRatio[模型] × groupRatio × QuotaPerUnit,
// 其中 **ModelRatio 在这里的语义被重定义为「美元/秒」**,不是 new-api 常规的
// 「每 token 的 quota」。原因是框架两处口径解不出同一个 ratio:
//
//	预扣      = ratio/2 × QuotaPerUnit   (relay/helper/price.go: ModelPriceHelperPerCall)
//	token重算 = tokens  × ratio          (service/task_billing.go: RecalculateTaskQuotaByTokens)
//
// 想让 5 秒 = $0.5,token 重算要求 ratio=50000,而那会让预扣变成 $2.5 万、请求直接卡死。
// 所以这里绕开 token 重算自己按秒算;ratio 兼作预扣的粗档(预扣≈ratio/2 美元)。
//
// ⚠️ 前提:该模型**不能**配 ModelPrice —— 配了会被 settleTaskBillingOnComplete
// 第 0 条「按次计费不做差额结算」直接短路,这个函数根本不会被调用。
//
// 返回 0 = 不调整,保持预扣额度。
func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int {
	if task == nil || taskResult == nil {
		return 0
	}
	// ParseTaskResult 往 TotalTokens 里塞的就是出片秒数
	seconds := taskResult.TotalTokens
	if seconds <= 0 {
		return 0
	}

	modelName := task.Properties.OriginModelName
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		modelName = bc.OriginModelName
	}
	pricePerSecond, ok, _ := ratio_setting.GetModelRatio(modelName)
	if !ok || pricePerSecond <= 0 {
		return 0
	}

	group := task.Group
	if group == "" {
		if user, err := model.GetUserById(task.UserId, false); err == nil {
			group = user.Group
		}
	}
	groupRatio := 1.0
	if group != "" {
		if userGroupRatio, has := ratio_setting.GetGroupGroupRatio(group, group); has {
			groupRatio = userGroupRatio
		} else {
			groupRatio = ratio_setting.GetGroupRatio(group)
		}
	}

	quota := int(float64(seconds) * pricePerSecond * groupRatio * common.QuotaPerUnit)
	if quota <= 0 {
		return 0
	}
	return quota
}

// billableSeconds 取计费秒数:优先上游 usage.total_seconds,缺失退回 output_seconds,
// 再退回请求时的 duration —— 保证任何情况下都不会算成 0 秒白嫖。
func billableSeconds(task *TaskObject) int {
	if task.Usage != nil {
		if task.Usage.TotalSeconds > 0 {
			return task.Usage.TotalSeconds
		}
		if task.Usage.OutputSeconds > 0 {
			return task.Usage.OutputSeconds
		}
	}
	if task.Duration > 0 {
		return task.Duration
	}
	return 0
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var resp QueryResponse
	if err := common.Unmarshal(originTask.Data, &resp); err != nil {
		return nil, errors.Wrap(err, "unmarshal minimax v2 task data failed")
	}

	openAIVideo := originTask.ToOpenAIVideo()
	if resp.Task != nil {
		if resp.Task.Duration > 0 {
			openAIVideo.Seconds = strconv.Itoa(resp.Task.Duration)
		}
		if resp.Task.Resolution != "" {
			openAIVideo.Size = resp.Task.Resolution
		}
		if resp.Task.Error != nil {
			openAIVideo.Error = &dto.OpenAIVideoError{
				Message: resp.Task.Error.String(),
				Code:    resp.Task.Error.Type,
			}
		}
	}
	if resp.Error != nil && openAIVideo.Error == nil {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: resp.Error.String(),
			Code:    resp.Error.Type,
		}
	}

	jsonData, err := common.Marshal(openAIVideo)
	if err != nil {
		return nil, errors.Wrap(err, "marshal openai video failed")
	}
	return jsonData, nil
}
