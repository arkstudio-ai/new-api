package volcengine

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

func TestBuildVolcengineASRRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	req, resourceID, err := buildVolcengineASRRequest(c, dto.AudioRequest{
		Model:          "seed-asr",
		AudioURL:       "https://example.com/audio.wav?x=1",
		ResponseFormat: "verbose_json",
	})
	if err != nil {
		t.Fatalf("buildVolcengineASRRequest() error = %v", err)
	}
	if resourceID != volcengineASRDefaultResourceID {
		t.Fatalf("resourceID = %q, want %q", resourceID, volcengineASRDefaultResourceID)
	}
	if req.Audio.URL != "https://example.com/audio.wav?x=1" {
		t.Fatalf("Audio.URL = %q", req.Audio.URL)
	}
	if req.Audio.Format != "wav" {
		t.Fatalf("Audio.Format = %q, want wav", req.Audio.Format)
	}
	if req.Request.ShowUtterances == nil || !*req.Request.ShowUtterances {
		t.Fatalf("ShowUtterances = %#v, want true", req.Request.ShowUtterances)
	}
}

func TestBuildVolcengineASRRequestMetadataOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)

	enablePunc := false
	req, resourceID, err := buildVolcengineASRRequest(c, dto.AudioRequest{
		Model:    "seed-asr",
		AudioURL: "https://example.com/audio.mp3",
		Metadata: []byte(`{
			"resource_id":"volc.bigasr.auc",
			"audio":{"format":"ogg","language":"en-US"},
			"request":{"model_name":"bigmodel","enable_punc":false}
		}`),
	})
	if err != nil {
		t.Fatalf("buildVolcengineASRRequest() error = %v", err)
	}
	if resourceID != "volc.bigasr.auc" {
		t.Fatalf("resourceID = %q", resourceID)
	}
	if req.Audio.URL != "https://example.com/audio.mp3" {
		t.Fatalf("metadata audio override dropped URL: %q", req.Audio.URL)
	}
	if req.Audio.Format != "ogg" {
		t.Fatalf("Audio.Format = %q, want ogg", req.Audio.Format)
	}
	if req.Audio.Language != "en-US" {
		t.Fatalf("Audio.Language = %q, want en-US", req.Audio.Language)
	}
	if req.Request.EnablePunc == nil || *req.Request.EnablePunc != enablePunc {
		t.Fatalf("EnablePunc = %#v, want false", req.Request.EnablePunc)
	}
}

func TestSetupVolcengineASRHeader(t *testing.T) {
	header := http.Header{}
	if err := setupVolcengineASRHeader(&header, "app|token|custom-resource", "request-id", volcengineASRDefaultResourceID, true); err != nil {
		t.Fatalf("setupVolcengineASRHeader() error = %v", err)
	}
	if header.Get("X-Api-App-Key") != "app" {
		t.Fatalf("X-Api-App-Key = %q", header.Get("X-Api-App-Key"))
	}
	if header.Get("X-Api-Access-Key") != "token" {
		t.Fatalf("X-Api-Access-Key = %q", header.Get("X-Api-Access-Key"))
	}
	if header.Get("X-Api-Resource-Id") != "custom-resource" {
		t.Fatalf("X-Api-Resource-Id = %q", header.Get("X-Api-Resource-Id"))
	}
	if header.Get("X-Api-Sequence") != "-1" {
		t.Fatalf("X-Api-Sequence = %q", header.Get("X-Api-Sequence"))
	}

	header = http.Header{}
	if err := setupVolcengineASRHeader(&header, "single-api-key", "request-id", volcengineASRDefaultResourceID, false); err != nil {
		t.Fatalf("setupVolcengineASRHeader() new-console error = %v", err)
	}
	if header.Get("X-Api-Key") != "single-api-key" {
		t.Fatalf("X-Api-Key = %q", header.Get("X-Api-Key"))
	}
	if header.Get("X-Api-Sequence") != "" {
		t.Fatalf("X-Api-Sequence = %q, want empty", header.Get("X-Api-Sequence"))
	}
}

func TestInferAudioFormat(t *testing.T) {
	tests := map[string]string{
		"https://example.com/a.mp3?sign=x": "mp3",
		"https://example.com/a.wav":        "wav",
		"https://example.com/a.ogg":        "ogg",
		"https://example.com/a.pcm":        "raw",
		"https://example.com/a":            "mp3",
	}
	for input, want := range tests {
		if got := inferAudioFormat(input); got != want {
			t.Fatalf("inferAudioFormat(%q) = %q, want %q", input, got, want)
		}
	}
}
