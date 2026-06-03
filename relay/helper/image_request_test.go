package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

func TestIsGPTImageModel(t *testing.T) {
	cases := map[string]bool{
		"gpt-image-1":           true,
		"gpt-image-2":           true,
		"GPT-IMAGE-2":           true,
		"chatgpt-image-latest":  true,
		"dall-e-3":              false,
		"wan2.7":                false,
	}
	for model, want := range cases {
		if got := IsGPTImageModel(model); got != want {
			t.Fatalf("IsGPTImageModel(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestSanitizeImageRequestForModel_stripsResponseFormat(t *testing.T) {
	req := &dto.ImageRequest{Model: "gpt-image-2", ResponseFormat: "url", Prompt: "x"}
	SanitizeImageRequestForModel(req)
	if req.ResponseFormat != "" {
		t.Fatalf("ResponseFormat = %q, want empty", req.ResponseFormat)
	}
}
