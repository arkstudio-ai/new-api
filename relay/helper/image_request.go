package helper

import (
	"strings"

	"github.com/QuantumNous/new-api/dto"
)

// IsGPTImageModel reports OpenAI GPT image series (gpt-image-1, gpt-image-2, gpt-image-1.5, ...).
// These models always return base64; response_format is dall-e only.
func IsGPTImageModel(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "gpt-image") || m == "chatgpt-image-latest"
}

// SanitizeImageRequestForModel strips fields upstream rejects for the given model family.
func SanitizeImageRequestForModel(req *dto.ImageRequest) {
	if req == nil || !IsGPTImageModel(req.Model) {
		return
	}
	req.ResponseFormat = ""
}
