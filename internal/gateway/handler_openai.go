package gateway

import (
	"net/http"

	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func (g *Gateway) dispatch(w http.ResponseWriter, r *http.Request, inFormat string, key *model.ExtKey, u *model.Upstream, realModel string, body []byte) {
	irReq, err := decodeInbound(body, inFormat)
	if err != nil {
		WriteError(w, 400, inFormat, "failed to decode request: "+err.Error(), "invalid_request_error")
		g.recordUsage(key, u, realModel, inFormat, 0, 0, false, "error")
		return
	}
	irReq.Model = realModel

	result, err := g.client.Call(r.Context(), u, irReq)
	if err != nil {
		if ue, ok := err.(*upstream.UpstreamError); ok {
			WriteError(w, ue.StatusCode, inFormat, string(ue.Body), "upstream_error")
		} else {
			WriteError(w, 502, inFormat, "upstream call failed: "+err.Error(), "upstream_error")
		}
		g.recordUsage(key, u, realModel, inFormat, 0, 0, irReq.Stream, "error")
		return
	}

	if result.Response != nil {
		g.handleNonStream(w, inFormat, result, key, u, realModel, irReq.Stream)
	} else {
		g.handleStream(w, inFormat, result, key, u, realModel)
	}
}

func (g *Gateway) handleNonStream(w http.ResponseWriter, inFormat string, result *upstream.Result, key *model.ExtKey, u *model.Upstream, realModel string, stream bool) {
	var out []byte
	var err error
	switch inFormat {
	case "anthropic":
		out, err = anthropic.EncodeResponse(result.Response)
	default:
		out, err = openai.EncodeResponse(result.Response)
	}
	if err != nil {
		WriteError(w, 500, inFormat, "failed to encode response", "internal_error")
		g.recordUsage(key, u, realModel, inFormat, result.Response.Usage.InputTokens, result.Response.Usage.OutputTokens, false, "error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
	usage := result.Usage()
	g.recordUsage(key, u, realModel, inFormat, usage.InputTokens, usage.OutputTokens, false, "ok")
}

func (g *Gateway) handleStream(w http.ResponseWriter, inFormat string, result *upstream.Result, key *model.ExtKey, u *model.Upstream, realModel string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, 500, inFormat, "streaming not supported", "internal_error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)

	var encoder interface {
		Encode(evt *translate.StreamEvent) ([][]byte, error)
	}
	if inFormat != "anthropic" {
		encoder = openai.NewStreamEncoder(realModel)
	}

	for ev := range result.Stream {
		var frames [][]byte
		var err error
		if inFormat == "anthropic" {
			f, e := anthropic.EncodeStreamEvent(ev)
			err = e
			if err == nil {
				frames = [][]byte{f}
			}
		} else {
			frames, err = encoder.Encode(ev)
		}
		if err != nil {
			continue
		}
		for _, f := range frames {
			w.Write(f)
		}
		flusher.Flush()
	}

	usage := result.Usage()
	g.recordUsage(key, u, realModel, inFormat, usage.InputTokens, usage.OutputTokens, true, "ok")
}

func decodeInbound(body []byte, inFormat string) (*translate.Request, error) {
	switch inFormat {
	case "anthropic":
		return anthropic.DecodeRequest(body)
	default:
		return openai.DecodeRequest(body)
	}
}

func (g *Gateway) recordUsage(key *model.ExtKey, u *model.Upstream, realModel, inFormat string, input, output int, stream bool, status string) {
	total := input + output
	rec := &model.UsageRecord{
		UpstreamName:     u.Name,
		Model:            realModel,
		InFormat:         inFormat,
		UpFormat:         u.Format,
		PromptTokens:     input,
		CompletionTokens: output,
		TotalTokens:      total,
		Stream:           stream,
		Status:           status,
	}
	if key != nil {
		kid := key.ID
		rec.ExtKeyID = &kid
	}
	if u != nil {
		uid := u.ID
		rec.UpstreamID = &uid
	}
	model.InsertUsage(g.db, rec)
}
