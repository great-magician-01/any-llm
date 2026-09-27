package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
)

// TestDecodeStreamEvent_Error 覆盖上游过载/超时的 event: error。解析器必须产出
// IR 的 error 事件；原实现没有 error 分支，直接落到末尾 return nil, nil ——
// 错误被静默吞掉，网关既不会给客户端错误帧也不知道流已经坏了。
func TestDecodeStreamEvent_Error(t *testing.T) {
	evt, err := DecodeStreamEvent(
		[]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if evt == nil {
		t.Fatal("error event was swallowed (nil, nil): the gateway would end the stream silently")
	}
	if evt.Type != "error" {
		t.Fatalf("type=%q want error", evt.Type)
	}
}

// TestDecodeStreamEvent_UnknownTypeIsIgnored 保证未知事件仍然被安静跳过
// （不能因为新增 error 分支就把未知类型当成错误）。
func TestDecodeStreamEvent_UnknownTypeIsIgnored(t *testing.T) {
	evt, err := DecodeStreamEvent([]byte(`{"type":"something_new","x":1}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if evt != nil {
		t.Fatalf("unknown event should be skipped, got %+v", evt)
	}
}

// TestEncodeBlocks_ImageSource 钉住 Anthropic 出站的 image source 形状：
// 内联数据 -> base64 source，纯 URL -> url source（而不是空 base64）。
func TestEncodeBlocks_ImageSource(t *testing.T) {
	t.Run("url", func(t *testing.T) {
		parts := encodeBlocks([]translate.ContentBlock{
			{Type: "image", Image: &translate.Image{URL: "https://x/a.png"}},
		})
		if len(parts) != 1 {
			t.Fatalf("parts=%d want 1", len(parts))
		}
		src, ok := parts[0]["source"].(map[string]any)
		if !ok {
			t.Fatalf("source missing: %+v", parts[0])
		}
		if src["type"] != "url" || src["url"] != "https://x/a.png" {
			t.Fatalf("source=%+v want {type:url url:https://x/a.png}", src)
		}
	})

	t.Run("base64", func(t *testing.T) {
		parts := encodeBlocks([]translate.ContentBlock{
			{Type: "image", Image: &translate.Image{Base64: "AAA", MediaType: "image/png"}},
		})
		src, ok := parts[0]["source"].(map[string]any)
		if !ok {
			t.Fatalf("source missing: %+v", parts[0])
		}
		if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "AAA" {
			t.Fatalf("source=%+v want {type:base64 media_type:image/png data:AAA}", src)
		}
	})

	t.Run("data_url_becomes_base64", func(t *testing.T) {
		parts := encodeBlocks([]translate.ContentBlock{
			{Type: "image", Image: translate.NewImage("data:image/jpeg;base64,QUJD")},
		})
		src, ok := parts[0]["source"].(map[string]any)
		if !ok {
			t.Fatalf("source missing: %+v", parts[0])
		}
		if src["type"] != "base64" || src["media_type"] != "image/jpeg" || src["data"] != "QUJD" {
			t.Fatalf("source=%+v want base64 image/jpeg QUJD", src)
		}
	})
}

// TestDecodeImagePart_URLSource 保证 {"type":"url"} 的图片 source 不会被丢成空图片。
func TestDecodeImagePart_URLSource(t *testing.T) {
	blocks, err := decodeBlocks(json.RawMessage(
		`[{"type":"image","source":{"type":"url","url":"https://x/a.png"}}]`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(blocks) != 1 || blocks[0].Image == nil {
		t.Fatalf("blocks=%+v", blocks)
	}
	if blocks[0].Image.URL != "https://x/a.png" {
		t.Fatalf("image=%+v want URL https://x/a.png", blocks[0].Image)
	}
}
