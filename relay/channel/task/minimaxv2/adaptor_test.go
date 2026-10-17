package minimaxv2

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestParseTaskResultSucceeded(t *testing.T) {
	body := []byte(`{"task":{"id":"424010985738629","model":"MiniMax-H3","status":"succeeded","created_at":1785125529,"updated_at":1785125946,"content":{"url":"https://cdn.hailuoai.com/a.mp4"},"resolution":"2K","duration":5,"usage":{"total_seconds":5,"input_seconds":0,"output_seconds":5,"input_image_count":0},"ratio":"16:9","task_type":"generation","modality":"video"}}`)
	a := &TaskAdaptor{}
	info, err := a.ParseTaskResult(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Status != model.TaskStatusSuccess {
		t.Fatalf("expected success, got %s", info.Status)
	}
	if info.Url != "https://cdn.hailuoai.com/a.mp4" {
		t.Fatalf("expected video url, got %q", info.Url)
	}
	if info.TaskID != "424010985738629" {
		t.Fatalf("unexpected task id %q", info.TaskID)
	}
	// 按秒计费:出片秒数进 TotalTokens
	if info.TotalTokens != 5 || info.CompletionTokens != 5 {
		t.Fatalf("expected 5 billable seconds, got total=%d completion=%d", info.TotalTokens, info.CompletionTokens)
	}
}

func TestBillableSecondsFallback(t *testing.T) {
	if got := billableSeconds(&TaskObject{Usage: &Usage{TotalSeconds: 8, OutputSeconds: 5}, Duration: 10}); got != 8 {
		t.Fatalf("total_seconds should win, got %d", got)
	}
	if got := billableSeconds(&TaskObject{Usage: &Usage{OutputSeconds: 5}, Duration: 10}); got != 5 {
		t.Fatalf("output_seconds should be second choice, got %d", got)
	}
	if got := billableSeconds(&TaskObject{Duration: 10}); got != 10 {
		t.Fatalf("duration should be last resort, got %d", got)
	}
	if got := billableSeconds(&TaskObject{}); got != 0 {
		t.Fatalf("expected 0 when nothing is known, got %d", got)
	}
}

// usage 缺失(上游偶发不回 usage)时不能白嫖,退回请求 duration
func TestParseTaskResultSucceededWithoutUsage(t *testing.T) {
	body := []byte(`{"task":{"id":"1","status":"succeeded","duration":9,"content":{"url":"https://x/a.mp4"}}}`)
	a := &TaskAdaptor{}
	info, err := a.ParseTaskResult(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.TotalTokens != 9 {
		t.Fatalf("expected fallback to duration 9, got %d", info.TotalTokens)
	}
}

func TestParseTaskResultRunning(t *testing.T) {
	body := []byte(`{"task":{"id":"2086717028399841280","model":"MiniMax-H3","status":"running","created_at":1786347084,"updated_at":1786347145,"resolution":"2K","duration":5,"usage":{"total_seconds":0},"ratio":"16:9","task_type":"generation","modality":"video","estimated_remaining_seconds":108}}`)
	a := &TaskAdaptor{}
	info, err := a.ParseTaskResult(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Status != model.TaskStatusInProgress {
		t.Fatalf("expected in progress, got %s", info.Status)
	}
}

func TestParseTaskResultQueuedAndFailed(t *testing.T) {
	a := &TaskAdaptor{}

	queued, err := a.ParseTaskResult([]byte(`{"task":{"id":"1","status":"queued"}}`))
	if err != nil || queued.Status != model.TaskStatusQueued {
		t.Fatalf("queued: %v %+v", err, queued)
	}

	failed, err := a.ParseTaskResult([]byte(`{"task":{"id":"1","status":"failed","error":{"type":"server_error","message":"boom","code":1027}}}`))
	if err != nil {
		t.Fatalf("failed: %v", err)
	}
	if failed.Status != model.TaskStatusFailure || failed.Reason != "boom (1027)" {
		t.Fatalf("unexpected failure info: %+v", failed)
	}
}

func TestParseTaskResultTopLevelError(t *testing.T) {
	body := []byte(`{"type":"error","error":{"type":"authorized_error","message":"invalid api key","http_code":"401"},"request_id":"x"}`)
	a := &TaskAdaptor{}
	info, err := a.ParseTaskResult(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Status != model.TaskStatusFailure || info.Reason != "invalid api key" {
		t.Fatalf("unexpected info: %+v", info)
	}
}

func TestBuildContent(t *testing.T) {
	req := &relaycommon.TaskSubmitReq{
		Prompt: "a cat",
		Images: []string{"https://x/a.png", "https://x/b.png"},
	}
	content := buildContent(req)
	if len(content) != 3 {
		t.Fatalf("expected 3 items, got %d", len(content))
	}
	if content[0].Type != ContentTypeText || content[0].Text != "a cat" {
		t.Fatalf("unexpected text item: %+v", content[0])
	}
	if content[1].Role != RoleFirstFrame || content[1].ImageURL.URL != "https://x/a.png" {
		t.Fatalf("unexpected first frame: %+v", content[1])
	}
	if content[2].Role != RoleReferenceImage {
		t.Fatalf("unexpected second image role: %+v", content[2])
	}
}

func TestParseResolutionAndRatio(t *testing.T) {
	cases := map[string]string{
		"":            DefaultResolution,
		"1920x1080":   Resolution2K,
		"2K":          Resolution2K,
		"1280x720":    Resolution768P,
		"768P":        Resolution768P,
		"512p":        Resolution512P,
		"480p":        Resolution480P,
		"whatever":    DefaultResolution,
	}
	for size, want := range cases {
		if size == "" {
			continue
		}
		if got := parseResolutionFromSize(size); got != want {
			t.Fatalf("size %q: expected %s, got %s", size, want, got)
		}
	}

	if got := parseRatioFromSize("1920x1080"); got != "16:9" {
		t.Fatalf("expected 16:9, got %s", got)
	}
	if got := parseRatioFromSize("1080x1920"); got != "9:16" {
		t.Fatalf("expected 9:16, got %s", got)
	}
	if got := parseRatioFromSize("garbage"); got != DefaultRatio {
		t.Fatalf("expected %s, got %s", DefaultRatio, got)
	}
}

// center 的 buildVideoGenerateRequestBody 用 metadata.content 整块下发多模态参考,
// 覆盖后必须把 prompt 文本补回来,否则 H3 直接 400。
func TestEnsureTextContentAfterMetadataOverride(t *testing.T) {
	overridden := []ContentItem{
		{Type: ContentTypeVideoURL, Role: RoleReferenceVideo, VideoURL: &MediaURL{URL: "https://x/a.mp4"}},
	}
	got := ensureTextContent(overridden, "口播文案")
	if len(got) != 2 {
		t.Fatalf("expected text prepended, got %d items", len(got))
	}
	if got[0].Type != ContentTypeText || got[0].Text != "口播文案" {
		t.Fatalf("expected text first, got %+v", got[0])
	}
	// 已有 text 时不重复插入
	if again := ensureTextContent(got, "口播文案"); len(again) != 2 {
		t.Fatalf("expected no duplicate text item, got %d", len(again))
	}
	// prompt 为空则原样返回(交给后续校验报错)
	if none := ensureTextContent(overridden, "  "); len(none) != 1 {
		t.Fatalf("expected untouched, got %d", len(none))
	}
}

func TestNormalizeResolution(t *testing.T) {
	cases := map[string]string{
		"":          DefaultResolution,
		"720p":      Resolution768P, // sd2 口径
		"768P":      Resolution768P,
		"480p":      Resolution480P,
		"512p":      Resolution512P,
		"1080p":     Resolution2K, // H3 没有 1080P 档,向上取
		"2K":        Resolution2K,
		"1920x1080": Resolution2K,
		"乱写":        DefaultResolution,
	}
	for in, want := range cases {
		if got := normalizeResolution(in); got != want {
			t.Fatalf("normalizeResolution(%q): expected %s, got %s", in, want, got)
		}
	}
}

func TestParseSeconds(t *testing.T) {
	if parseSeconds("5s") != 5 || parseSeconds("8") != 8 || parseSeconds("") != 0 || parseSeconds("abc") != 0 {
		t.Fatal("parseSeconds mismatch")
	}
}
