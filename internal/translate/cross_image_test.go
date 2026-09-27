package translate_test

import (
	"testing"

	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
	"github.com/great-magician-01/any-llm/internal/translate/responses"
)

// TestCrossImage_PayloadPreserved 是图片载荷的跨格式保真矩阵（三格式两两互转）：
// URL 图片在任何一侧都必须是 URL，内联 base64 必须仍是同一份 base64 + media_type。
//
// 回归位：曾经 URL-only 图片编到 Anthropic 会变成
// {"type":"base64","media_type":"","data":""}（上游必然 400）；反向 base64 图片
// 编到 OpenAI/Responses 会变成空 url。两者都没有测试，因为 cross_test.go 用一个
// "not preserved by design" 的空 case 把断言豁免掉了。
func TestCrossImage_PayloadPreserved(t *testing.T) {
	const (
		urlImage  = "https://example.com/a.png"
		b64       = "iVBORw0KGgoAAAANSUhEUg=="
		mediaType = "image/png"
	)

	// 同一张图在三种格式下的原始表达：一张 URL 图 + 一张内联 base64 图。
	sources := map[string]string{
		"openai": `{"model":"m","messages":[{"role":"user","content":[` +
			`{"type":"image_url","image_url":{"url":"` + urlImage + `"}},` +
			`{"type":"image_url","image_url":{"url":"data:` + mediaType + `;base64,` + b64 + `"}}]}]}`,
		"anthropic": `{"model":"m","max_tokens":8,"messages":[{"role":"user","content":[` +
			`{"type":"image","source":{"type":"url","url":"` + urlImage + `"}},` +
			`{"type":"image","source":{"type":"base64","media_type":"` + mediaType + `","data":"` + b64 + `"}}]}]}`,
		"responses": `{"model":"m","input":[{"role":"user","content":[` +
			`{"type":"input_image","image_url":"` + urlImage + `"},` +
			`{"type":"input_image","image_url":"data:` + mediaType + `;base64,` + b64 + `"}]}]}`,
	}

	decode := func(t *testing.T, format, body string) *translate.Request {
		t.Helper()
		var (
			req *translate.Request
			err error
		)
		switch format {
		case "openai":
			req, err = openai.DecodeRequest([]byte(body))
		case "anthropic":
			req, err = anthropic.DecodeRequest([]byte(body))
		case "responses":
			req, err = responses.DecodeRequest([]byte(body))
		default:
			t.Fatalf("unknown format %q", format)
		}
		if err != nil {
			t.Fatalf("%s decode: %v", format, err)
		}
		return req
	}

	encode := func(t *testing.T, format string, req *translate.Request) []byte {
		t.Helper()
		var (
			out []byte
			err error
		)
		switch format {
		case "openai":
			out, err = openai.EncodeRequest(req)
		case "anthropic":
			out, err = anthropic.EncodeRequest(req)
		case "responses":
			out, err = responses.EncodeRequest(req)
		default:
			t.Fatalf("unknown format %q", format)
		}
		if err != nil {
			t.Fatalf("%s encode: %v", format, err)
		}
		return out
	}

	formats := []string{"openai", "anthropic", "responses"}
	for _, src := range formats {
		for _, dst := range formats {
			t.Run(src+"_to_"+dst, func(t *testing.T) {
				ir1 := decode(t, src, sources[src])
				assertRequestImages(t, src, ir1, urlImage, b64, mediaType)

				ir2 := decode(t, dst, string(encode(t, dst, ir1)))
				assertRequestImages(t, src+"→"+dst, ir2, urlImage, b64, mediaType)
			})
		}
	}
}

// assertRequestImages 断言请求里恰好有 URL 图与 base64 图各一张，且两者都保真。
func assertRequestImages(t *testing.T, label string, req *translate.Request, urlImage, b64, mediaType string) {
	t.Helper()
	var imgs []*translate.Image
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == "image" {
				imgs = append(imgs, b.Image)
			}
		}
	}
	if len(imgs) != 2 {
		t.Fatalf("%s: image count=%d want 2 (messages=%+v)", label, len(imgs), req.Messages)
	}
	if imgs[0] == nil || imgs[0].URL != urlImage || imgs[0].Base64 != "" {
		t.Errorf("%s: URL 图片变成 %+v，期望 URL=%q 且无内联数据", label, imgs[0], urlImage)
	}
	if imgs[1] == nil || imgs[1].Base64 != b64 || imgs[1].MediaType != mediaType {
		t.Errorf("%s: base64 图片变成 %+v，期望 base64=%q media_type=%q", label, imgs[1], b64, mediaType)
	}
}
