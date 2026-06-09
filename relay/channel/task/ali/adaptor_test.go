package ali

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// 模拟商业版网关把 DashScope 原生 input(含 media[]) + parameters 嵌套放进 metadata 透传。
// 期望:上游请求体里 input.media 被原样带上,且不会残留冲突的 size/resolution。
func TestConvertToAliRequest_NativeMediaPassthrough(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Prompt: "a cat dancing",
		Model:  "wan2.7-r2v",
		Metadata: map[string]interface{}{
			"input": map[string]interface{}{
				"prompt": "a cat dancing",
				"media": []map[string]interface{}{
					{"type": "reference_video", "url": "https://oss.example.com/ref.mp4"},
					{"type": "reference_image", "url": "https://oss.example.com/ref.png"},
				},
			},
			"parameters": map[string]interface{}{
				"resolution": "1080P",
				"ratio":      "16:9",
				"duration":   5,
			},
		},
	}

	aliReq, err := a.convertToAliRequest(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, req)
	if err != nil {
		t.Fatalf("convertToAliRequest failed: %v", err)
	}

	if len(aliReq.Input.Media) != 2 {
		t.Fatalf("expected 2 media items, got %d (%+v)", len(aliReq.Input.Media), aliReq.Input)
	}
	if aliReq.Input.Media[0].Type != "reference_video" || aliReq.Input.Media[0].URL != "https://oss.example.com/ref.mp4" {
		t.Errorf("unexpected first media item: %+v", aliReq.Input.Media[0])
	}
	if aliReq.Parameters.Resolution != "1080P" || aliReq.Parameters.Ratio != "16:9" {
		t.Errorf("native parameters not applied: %+v", aliReq.Parameters)
	}
	// native 路径必须跳过默认推断 —— 不能残留 size,否则与 media[] 请求冲突。
	if aliReq.Parameters.Size != "" {
		t.Errorf("expected empty size on native path, got %q", aliReq.Parameters.Size)
	}

	// 最终序列化必须包含 input.media(上游报 "Field required: input.media" 的根因)。
	body, err := common.Marshal(aliReq)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var parsed map[string]interface{}
	if err := common.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	input, _ := parsed["input"].(map[string]interface{})
	if input == nil || input["media"] == nil {
		t.Fatalf("serialized body missing input.media: %s", body)
	}
}

// 旧的 legacy 路径(无 native metadata)必须保持原状:按模型推断默认分辨率。
func TestConvertToAliRequest_LegacyDefaultsUnchanged(t *testing.T) {
	a := &TaskAdaptor{}
	req := relaycommon.TaskSubmitReq{
		Prompt: "hello",
		Model:  "wan2.2-i2v-plus",
	}
	aliReq, err := a.convertToAliRequest(&relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}, req)
	if err != nil {
		t.Fatalf("convertToAliRequest failed: %v", err)
	}
	if aliReq.Parameters.Resolution != "1080P" {
		t.Errorf("expected legacy default resolution 1080P, got %q", aliReq.Parameters.Resolution)
	}
	if aliReq.Parameters.Duration != 5 {
		t.Errorf("expected legacy default duration 5, got %d", aliReq.Parameters.Duration)
	}
	if len(aliReq.Input.Media) != 0 {
		t.Errorf("legacy path should have no media, got %+v", aliReq.Input.Media)
	}
}
