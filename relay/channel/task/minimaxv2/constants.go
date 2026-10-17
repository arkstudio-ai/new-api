package minimaxv2

// MiniMax 视频生成 V2（MiniMax-H3）。
// 与 v1（relay/channel/task/hailuo）完全不同的协议：
//   - 提交：POST {base}/v2/video_generation，多模态 content 数组
//   - 查询：GET  {base}/v2/query/video_generation/{task_id}，返回 {"task": {...}}
//   - 成品：task.content.url 直接给出限时下载地址，无需 v1 的 file_id 二次换取
//
// docs: https://platform.minimaxi.com/docs/api-reference/video-generation-v2-create
//
//	https://platform.minimaxi.com/docs/api-reference/video-generation-v2-query
const ChannelName = "minimax-v2"

var ModelList = []string{
	"MiniMax-H3",
}

const (
	GenerateEndpoint = "/v2/video_generation"
	QueryEndpoint    = "/v2/query/video_generation"
)

// 上游接受的分辨率档位（官方文档为 768P / 2K，实测网关另放行 480p / 512p）
const (
	Resolution480P = "480p"
	Resolution512P = "512p"
	Resolution768P = "768P"
	Resolution2K   = "2K"
)

// task.status 取值
const (
	TaskStatusQueued    = "queued"
	TaskStatusRunning   = "running"
	TaskStatusSucceeded = "succeeded"
	TaskStatusFailed    = "failed"
	TaskStatusCancelled = "cancelled"
)

// content[].type
const (
	ContentTypeText     = "text"
	ContentTypeImageURL = "image_url"
	ContentTypeVideoURL = "video_url"
	ContentTypeAudioURL = "audio_url"
)

// content[].role
const (
	RoleFirstFrame     = "first_frame"
	RoleLastFrame      = "last_frame"
	RoleReferenceImage = "reference_image"
	RoleReferenceVideo = "reference_video"
	RoleReferenceAudio = "reference_audio"
)

const (
	DefaultDuration   = 5
	DefaultResolution = Resolution768P
	DefaultRatio      = "adaptive"
	MinDuration       = 4
	// 2026-10-10: metaso 网关（渠道 114 上游）控制台与 API 均放行到 30 秒（直连实测 duration=30 接受）；
	// MiniMax 官方 H3 仍是 4~15，所以上限随本网关走。
	MaxDuration = 30
)
