package minimaxv2

import "fmt"

type MediaURL struct {
	URL string `json:"url"`
}

type ContentItem struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	Role     string    `json:"role,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
}

type VideoRequest struct {
	Model         string        `json:"model"`
	Content       []ContentItem `json:"content"`
	Resolution    string        `json:"resolution,omitempty"`
	Duration      *int          `json:"duration,omitempty"`
	Ratio         string        `json:"ratio,omitempty"`
	CallbackURL   string        `json:"callback_url,omitempty"`
	AigcWatermark *bool         `json:"aigc_watermark,omitempty"`
}

// APIError 是 v2 的统一错误体：
// {"type":"error","error":{"type":"...","message":"...","http_code":"400"},"request_id":"..."}
type APIError struct {
	Type     string `json:"type,omitempty"`
	Message  string `json:"message,omitempty"`
	HTTPCode string `json:"http_code,omitempty"`
	// 任务对象内的 error 结构未在文档中固定，用 any 兜住数字/字符串两种 code
	Code any `json:"code,omitempty"`
}

func (e *APIError) String() string {
	if e == nil {
		return ""
	}
	msg := e.Message
	if msg == "" {
		msg = e.Type
	}
	if e.Code != nil {
		return fmt.Sprintf("%s (%v)", msg, e.Code)
	}
	return msg
}

type SubmitResponse struct {
	TaskID    string    `json:"task_id"`
	Type      string    `json:"type,omitempty"`
	Error     *APIError `json:"error,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
}

type Usage struct {
	TotalSeconds    int `json:"total_seconds"`
	InputSeconds    int `json:"input_seconds"`
	OutputSeconds   int `json:"output_seconds"`
	InputImageCount int `json:"input_image_count"`
}

type TaskContent struct {
	URL    string `json:"url,omitempty"`
	Prompt string `json:"prompt,omitempty"`
}

type TaskObject struct {
	ID                        string       `json:"id"`
	Model                     string       `json:"model,omitempty"`
	Status                    string       `json:"status"`
	CreatedAt                 int64        `json:"created_at,omitempty"`
	UpdatedAt                 int64        `json:"updated_at,omitempty"`
	Content                   *TaskContent `json:"content,omitempty"`
	Resolution                string       `json:"resolution,omitempty"`
	Duration                  int          `json:"duration,omitempty"`
	Ratio                     string       `json:"ratio,omitempty"`
	TaskType                  string       `json:"task_type,omitempty"`
	Modality                  string       `json:"modality,omitempty"`
	Usage                     *Usage       `json:"usage,omitempty"`
	EstimatedRemainingSeconds int          `json:"estimated_remaining_seconds,omitempty"`
	Error                     *APIError    `json:"error,omitempty"`
}

type QueryResponse struct {
	Task      *TaskObject `json:"task"`
	Type      string      `json:"type,omitempty"`
	Error     *APIError   `json:"error,omitempty"`
	RequestID string      `json:"request_id,omitempty"`
}
