package tokenhubportrait

import (
	"context"
	"strings"
	"time"
)

type reference struct {
	kind, source string
	replace      func(string)
}

// PrepareVideo preserves the client's wire shape, roles and generation options.
// Both legacy reference fields and content[] are rewritten; duplicate mentions
// of one source are prepared once. The original request body is never mutated.
func (c *Client) PrepareVideo(ctx context.Context, body map[string]any) error {
	var refs []reference
	collectURL := func(parent map[string]any, key, kind string) error {
		value, exists := parent[key]
		if !exists || value == nil {
			return nil
		}
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return Fail("portrait_reference_invalid", "参考素材地址不能为空。", 400)
			}
			refs = append(refs, reference{kind, v, func(uri string) { parent[key] = uri }})
		case map[string]any:
			source, ok := v["url"].(string)
			if !ok || strings.TrimSpace(source) == "" {
				return Fail("portrait_reference_invalid", "参考素材需要有效的 url 字段。", 400)
			}
			refs = append(refs, reference{kind, source, func(uri string) { v["url"] = uri }})
		default:
			return Fail("portrait_reference_invalid", "参考素材地址格式无效。", 400)
		}
		return nil
	}
	content := func(parent map[string]any) error {
		value, exists := parent["content"]
		if !exists {
			return nil
		}
		items, ok := value.([]any)
		if !ok {
			return Fail("portrait_reference_invalid", "content 必须是数组。", 400)
		}
		for _, item := range items {
			m, ok := item.(map[string]any)
			if !ok {
				return Fail("portrait_reference_invalid", "content 元素必须是对象。", 400)
			}
			for _, field := range []struct{ key, kind string }{{"image_url", "Image"}, {"video_url", "Video"}, {"audio_url", "Audio"}} {
				if err := collectURL(m, field.key, field.kind); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := content(body); err != nil {
		return err
	}
	metadata, hasMetadata := body["metadata"].(map[string]any)
	if _, exists := body["metadata"]; exists && !hasMetadata {
		return Fail("portrait_reference_invalid", "metadata 必须是对象。", 400)
	}
	if hasMetadata {
		if err := content(metadata); err != nil {
			return err
		}
	}
	for _, field := range []struct{ key, kind string }{{"image_url", "Image"}, {"video_url", "Video"}, {"audio_url", "Audio"}} {
		if err := collectURL(body, field.key, field.kind); err != nil {
			return err
		}
	}
	for _, field := range []struct{ key, kind string }{{"images", "Image"}, {"extra_images", "Image"}, {"extra_videos", "Video"}, {"extra_audios", "Audio"}} {
		value, exists := body[field.key]
		if !exists {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return Fail("portrait_reference_invalid", "参考素材列表必须是数组。", 400)
		}
		for i, item := range items {
			source, ok := item.(string)
			if !ok || source == "" {
				return Fail("portrait_reference_invalid", "参考素材列表需包含有效的文件地址。", 400)
			}
			idx := i
			refs = append(refs, reference{field.kind, source, func(uri string) { items[idx] = uri }})
		}
	}
	if len(refs) > 32 {
		return Fail("portrait_reference_limit", "参考素材数量过多。", 400)
	}
	group := String(metadata, "portrait_group_id")
	if hasMetadata {
		delete(metadata, "portrait_group_id")
	}
	if len(refs) == 0 {
		return nil
	}
	if _, err := c.OwnGroup(ctx, group); err != nil {
		return err
	}
	// Bound the entire preparation, not thirty seconds per reference. If it
	// expires, persisted asset IDs are reused on the next preparation attempt.
	ctx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()
	prepared := map[string]string{}
	for _, ref := range refs {
		key := ref.kind + "\n" + ref.source
		uri, ok := prepared[key]
		if !ok {
			asset, err := c.EnsureAsset(ctx, group, ref.source, ref.kind, 30*time.Second)
			if err != nil {
				return err
			}
			uri = "asset://" + asset.AssetId
			prepared[key] = uri
		}
		ref.replace(uri)
	}
	return nil
}
