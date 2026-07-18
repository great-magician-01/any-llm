package gateway

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

func (g *Gateway) dispatch(w http.ResponseWriter, r *http.Request, inFormat string, key *model.ExtKey, u *model.Upstream, realModel string, body []byte) {
	logger.Info("completion request",
		"key_id", key.ID,
		"key_label", key.Label,
		"upstream", u.Name,
		"upstream_format", u.Format,
		"model", realModel,
		"in_format", inFormat,
		"stream", bodyHasStream(body),
	)
	irReq, err := decodeInbound(body, inFormat)
	if err != nil {
		WriteError(w, 400, inFormat, "failed to decode request: "+err.Error(), "invalid_request_error")
		g.recordUsage(key, u, realModel, inFormat, 0, 0, false, "error")
		return
	}
	irReq.Model = realModel

	if irReq.Stream {
		g.handleStream(w, r, inFormat, key, u, realModel, irReq)
		return
	}

	result, err := g.client.Call(r.Context(), u, irReq)
	if err != nil {
		if ue, ok := err.(*upstream.UpstreamError); ok {
			WriteError(w, ue.StatusCode, inFormat, ue.Message(), mapErrorType(inFormat, ue.StatusCode, ue.ErrorType()))
		} else {
			WriteError(w, 502, inFormat, "upstream call failed: "+err.Error(), "upstream_error")
		}
		g.recordUsage(key, u, realModel, inFormat, 0, 0, irReq.Stream, "error")
		return
	}

	g.handleNonStream(w, inFormat, result, key, u, realModel, irReq.Stream)
}

func bodyHasStream(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.Stream
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
	logger.Info("completion done",
		"upstream", u.Name,
		"model", realModel,
		"stream", false,
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"status", "ok",
	)
}

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request, inFormat string, key *model.ExtKey, u *model.Upstream, realModel string, irReq *translate.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, 500, inFormat, "streaming not supported", "internal_error")
		g.recordUsage(key, u, realModel, inFormat, 0, 0, true, "error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	flusher.Flush()
	streamStart := time.Now()
	logger.Info("stream header flushed", "upstream", u.Name, "model", realModel)

	var encoder interface {
		Encode(evt *translate.StreamEvent) ([][]byte, error)
	}
	if inFormat != "anthropic" {
		encoder = openai.NewStreamEncoder(realModel)
	}

	pingCount := 0
	writePing := func() {
		if inFormat == "anthropic" {
			if f, err := anthropic.EncodeStreamEvent(&translate.StreamEvent{Type: "ping"}); err == nil {
				w.Write(f)
			}
		} else {
			w.Write([]byte(": kp\n"))
		}
		flusher.Flush()
		pingCount++
		logger.Info("ping sent", "n", pingCount, "elapsed_ms", time.Since(streamStart).Milliseconds())
	}

	type callRet struct {
		result *upstream.Result
		err    error
	}
	callCh := make(chan callRet, 1)
	go func() {
		result, err := g.client.Call(r.Context(), u, irReq)
		callCh <- callRet{result, err}
	}()

	keepalive := time.NewTicker(500 * time.Millisecond)
	defer keepalive.Stop()

	var result *upstream.Result
	var callErr error
	callDone := false
	clientGone := false

	for !callDone {
		select {
		case ret := <-callCh:
			result, callErr = ret.result, ret.err
			callDone = true
			logger.Info("call returned", "elapsed_ms", time.Since(streamStart).Milliseconds(), "err", callErr)
		case <-keepalive.C:
			writePing()
		case <-r.Context().Done():
			clientGone = true
			callDone = true
			logger.Info("client context done (before call returned)", "elapsed_ms", time.Since(streamStart).Milliseconds())
		}
	}

	if clientGone {
		g.recordUsage(key, u, realModel, inFormat, 0, 0, true, "error")
		logger.Info("completion done",
			"upstream", u.Name, "model", realModel, "stream", true,
			"input_tokens", 0, "output_tokens", 0, "status", "error", "reason", "client_gone_before_call_done",
		)
		return
	}

	if callErr != nil {
		var msg, errType string
		var status int
		if ue, ok := callErr.(*upstream.UpstreamError); ok {
			msg, errType, status = ue.Message(), mapErrorType(inFormat, ue.StatusCode, ue.ErrorType()), ue.StatusCode
		} else {
			msg, errType, status = "upstream call failed: "+callErr.Error(), "upstream_error", 502
		}
		logger.Warn("upstream call failed after stream header sent",
			"upstream", u.Name, "model", realModel, "status", status, "err", msg)
		if inFormat == "anthropic" {
			payload, _ := json.Marshal(map[string]any{
				"type":  "error",
				"error": map[string]any{"type": errType, "message": msg},
			})
			w.Write([]byte("event: error\ndata: " + string(payload) + "\n\n"))
		} else {
			payload, _ := json.Marshal(map[string]any{
				"error": map[string]any{"message": msg, "type": errType},
			})
			w.Write([]byte("data: " + string(payload) + "\n\n"))
		}
		flusher.Flush()
		g.recordUsage(key, u, realModel, inFormat, 0, 0, true, "error")
		logger.Info("completion done",
			"upstream", u.Name, "model", realModel, "stream", true,
			"input_tokens", 0, "output_tokens", 0, "status", "error",
		)
		return
	}

	if result.Response != nil {
		var out []byte
		var encErr error
		switch inFormat {
		case "anthropic":
			out, encErr = anthropic.EncodeResponse(result.Response)
		default:
			out, encErr = openai.EncodeResponse(result.Response)
		}
		if encErr != nil {
			g.recordUsage(key, u, realModel, inFormat, result.Response.Usage.InputTokens, result.Response.Usage.OutputTokens, true, "error")
			return
		}
		w.Write(out)
		flusher.Flush()
		usage := result.Usage()
		g.recordUsage(key, u, realModel, inFormat, usage.InputTokens, usage.OutputTokens, true, "ok")
		logger.Info("completion done",
			"upstream", u.Name, "model", realModel, "stream", true,
			"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "status", "ok",
		)
		return
	}

	logger.Info("entering post-call stream loop", "elapsed_ms", time.Since(streamStart).Milliseconds())
	blockStarted := make(map[int]bool)
	for {
		select {
		case ev, ok := <-result.Stream:
			if !ok {
				goto done
			}
			logger.Info("upstream event", "type", ev.Type, "index", ev.Index, "elapsed_ms", time.Since(streamStart).Milliseconds())
			// Synthesize content_block_start if upstream omitted it (e.g. deepseek).
			// Without this, Anthropic SDK aborts on receiving content_block_delta
			// for an index that never had content_block_start.
			if inFormat == "anthropic" && ev.Type == "content_block_delta" && !blockStarted[ev.Index] {
				blockType := "text"
				if ev.Delta != nil {
					switch ev.Delta.Type {
					case "input_json_delta":
						blockType = "tool_use"
					case "thinking_delta", "signature_delta":
						blockType = "thinking"
					}
				}
				synthEv := &translate.StreamEvent{
					Type:  "content_block_start",
					Index: ev.Index,
					Block: &translate.ContentBlock{Type: blockType},
				}
				if f, e := anthropic.EncodeStreamEvent(synthEv); e == nil {
					w.Write(f)
					logger.Info("synthesized content_block_start", "index", ev.Index, "block_type", blockType)
				}
				blockStarted[ev.Index] = true
			}
			if ev.Type == "content_block_start" {
				blockStarted[ev.Index] = true
			}
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
		case <-keepalive.C:
			writePing()
		}
	}
done:

	usage := result.Usage()
	status := "ok"
	if err := result.StreamErr(); err != nil {
		status = "error"
		logger.Warn("stream ended with error", "upstream", u.Name, "model", realModel, "err", err)
	}
	g.recordUsage(key, u, realModel, inFormat, usage.InputTokens, usage.OutputTokens, true, status)
	logger.Info("completion done",
		"upstream", u.Name,
		"model", realModel,
		"stream", true,
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"status", status,
	)
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
	if g.writer != nil {
		g.writer.DoAsync(func(d *sql.DB) error { return model.InsertUsage(d, rec) })
	} else {
		model.InsertUsage(g.db, rec)
	}
}
