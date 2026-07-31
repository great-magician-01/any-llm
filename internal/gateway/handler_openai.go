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
	"github.com/great-magician-01/any-llm/internal/translate/responses"
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
		g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, false, "error")
		return
	}
	irReq.Model = realModel

	var sess *sessionCtx
	if inFormat == "responses" {
		sess = &sessionCtx{respID: responses.NewID(), input: irReq.Messages}
		if pid, _ := irReq.Extra["previous_response_id"].(string); pid != "" {
			hist, ok, err := g.sessions.Get(pid)
			if err != nil {
				WriteError(w, 500, inFormat, "session lookup failed: "+err.Error(), "internal_error")
				g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, false, "error")
				return
			}
			if !ok {
				WriteError(w, 400, inFormat, "unknown previous_response_id: "+pid, "invalid_previous_response_id")
				g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, false, "error")
				return
			}
			sess.prev = hist
			// 注意拷贝：避免 append 复写 hist 底层数组
			merged := make([]translate.Message, 0, len(hist)+len(irReq.Messages))
			merged = append(merged, hist...)
			merged = append(merged, irReq.Messages...)
			irReq.Messages = merged
		}
		// 会话字段不转发给上游
		delete(irReq.Extra, "previous_response_id")
		delete(irReq.Extra, "store")
	}

	if irReq.Stream {
		g.handleStream(w, r, inFormat, key, u, realModel, irReq, sess)
		return
	}

	result, err := g.client.Call(r.Context(), u, irReq, r.Header)
	if err != nil {
		if ue, ok := err.(*upstream.UpstreamError); ok {
			WriteError(w, ue.StatusCode, inFormat, ue.Message(), mapErrorType(inFormat, ue.StatusCode, ue.ErrorType()))
		} else {
			WriteError(w, 502, inFormat, "upstream call failed: "+err.Error(), "upstream_error")
		}
		g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, irReq.Stream, "error")
		return
	}

	if sess != nil {
		result.Response.ID = sess.respID
	}
	g.handleNonStream(w, inFormat, result, key, u, realModel, irReq.Stream, sess)
}

func bodyHasStream(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.Stream
}

func (g *Gateway) handleNonStream(w http.ResponseWriter, inFormat string, result *upstream.Result, key *model.ExtKey, u *model.Upstream, realModel string, stream bool, sess *sessionCtx) {
	var out []byte
	var err error
	switch inFormat {
	case "anthropic":
		out, err = anthropic.EncodeResponse(result.Response)
	case "responses":
		out, err = responses.EncodeResponse(result.Response)
	default:
		out, err = openai.EncodeResponse(result.Response)
	}
	if err != nil {
		WriteError(w, 500, inFormat, "failed to encode response", "internal_error")
		logger.Error("non-stream encode failed", "in_format", inFormat, "err", err)
		g.recordUsage(key, u, realModel, inFormat, result.Response.Usage, false, "error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
	if sess != nil {
		g.saveSession(sess, result.Response.Content)
	}
	usage := result.Usage()
	g.recordUsage(key, u, realModel, inFormat, usage, false, "ok")
	logger.Info("completion done",
		"upstream", u.Name,
		"model", realModel,
		"stream", false,
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"status", "ok",
	)
}

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request, inFormat string, key *model.ExtKey, u *model.Upstream, realModel string, irReq *translate.Request, sess *sessionCtx) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, 500, inFormat, "streaming not supported", "internal_error")
		g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, true, "error")
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
	switch inFormat {
	case "anthropic":
		encoder = nil
	case "responses":
		encoder = responses.NewStreamEncoder(realModel, sess.respID)
	default:
		encoder = openai.NewStreamEncoder(realModel)
	}

	pingCount := 0
	writePing := func() {
		if inFormat == "anthropic" {
			if f, err := anthropic.EncodeStreamEvent(&translate.StreamEvent{Type: "ping"}); err == nil {
				w.Write(f)
			} else {
				logger.Warn("stream anthropic ping encode failed", "err", err)
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
		result, err := g.client.Call(r.Context(), u, irReq, r.Header)
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
			if callErr != nil {
				logger.Info("call returned", "elapsed_ms", time.Since(streamStart).Milliseconds(), "err", callErr)
			} else {
				logger.Info("call returned", "elapsed_ms", time.Since(streamStart).Milliseconds())
			}
		case <-keepalive.C:
			writePing()
		case <-r.Context().Done():
			clientGone = true
			callDone = true
			logger.Info("client context done (before call returned)", "elapsed_ms", time.Since(streamStart).Milliseconds())
		}
	}

	if clientGone {
		g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, true, "error")
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
		} else if inFormat == "responses" {
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
		g.recordUsage(key, u, realModel, inFormat, translate.Usage{}, true, "error")
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
			logger.Error("stream non-stream response encode failed", "in_format", inFormat, "err", encErr)
			g.recordUsage(key, u, realModel, inFormat, result.Response.Usage, true, "error")
			return
		}
		w.Write(out)
		flusher.Flush()
		usage := result.Usage()
		g.recordUsage(key, u, realModel, inFormat, usage, true, "ok")
		logger.Info("completion done",
			"upstream", u.Name, "model", realModel, "stream", true,
			"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "status", "ok",
		)
		return
	}

	logger.Info("entering post-call stream loop", "elapsed_ms", time.Since(streamStart).Milliseconds())
	blockStarted := make(map[int]bool)
	clientGonePost := false
	for {
		select {
		case ev, ok := <-result.Stream:
			if !ok {
				goto done
			}
			logger.FileOnly().Info("upstream event", "type", ev.Type, "index", ev.Index, "elapsed_ms", time.Since(streamStart).Milliseconds())
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
				logger.Warn("stream frame encode skipped", "in_format", inFormat, "type", ev.Type, "index", ev.Index, "err", err)
				continue
			}
			for _, f := range frames {
				w.Write(f)
			}
			flusher.Flush()
		case <-keepalive.C:
			writePing()
		case <-r.Context().Done():
			clientGonePost = true
			logger.Info("client context done (during stream)", "elapsed_ms", time.Since(streamStart).Milliseconds())
			goto done
		}
	}
done:
	// 让 responses 编码器补发 response.completed（上游若没发 message_stop），
	// 成功后把累积输出写入会话存储。
	if !clientGonePost {
		if enc, ok := encoder.(interface {
			Flush() [][]byte
			Content() []translate.ContentBlock
		}); ok {
			if frames := enc.Flush(); len(frames) > 0 {
				for _, f := range frames {
					w.Write(f)
				}
				flusher.Flush()
			}
			if sess != nil {
				g.saveSession(sess, enc.Content())
			}
		}
	}

	usage := result.Usage()
	status := "ok"
	if clientGonePost {
		status = "error"
	} else if err := result.StreamErr(); err != nil {
		status = "error"
		logger.Warn("stream ended with error", "upstream", u.Name, "model", realModel, "err", err)
	}
	g.recordUsage(key, u, realModel, inFormat, usage, true, status)
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
	case "responses":
		return responses.DecodeRequest(body)
	default:
		return openai.DecodeRequest(body)
	}
}

func (g *Gateway) recordUsage(key *model.ExtKey, u *model.Upstream, realModel, inFormat string, usage translate.Usage, stream bool, status string) {
	total := usage.InputTokens + usage.OutputTokens
	rec := &model.UsageRecord{
		UpstreamName:      u.Name,
		Model:             realModel,
		InFormat:          inFormat,
		UpFormat:          u.Format,
		PromptTokens:      usage.InputTokens,
		CompletionTokens:  usage.OutputTokens,
		TotalTokens:       total,
		CacheReadTokens:   usage.CacheReadTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		ReasoningTokens:   usage.ReasoningTokens,
		Stream:            stream,
		Status:            status,
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
		if err := model.InsertUsage(g.db, rec); err != nil {
			logger.Error("record usage sync write failed", "key_id", rec.ExtKeyID, "upstream", rec.UpstreamName, "model", rec.Model, "total_tokens", rec.TotalTokens, "err", err)
		}
	}
}

// saveSession 累积会话：旧历史 + 本轮输入 + 本轮模型输出。
// 只在调用成功后调用；失败时不存，客户端带同一 previous_response_id 重试不会重复。
func (g *Gateway) saveSession(sess *sessionCtx, content []translate.ContentBlock) {
	msgs := make([]translate.Message, 0, len(sess.prev)+len(sess.input)+1)
	msgs = append(msgs, sess.prev...)
	msgs = append(msgs, sess.input...)
	msgs = append(msgs, translate.Message{Role: "assistant", Content: content})
	if err := g.sessions.Put(sess.respID, msgs); err != nil {
		logger.Warn("session save failed", "id", sess.respID, "err", err)
	}
}

type sessionCtx struct {
	respID string               // 返回给客户端的响应 id，也是会话 key
	prev   []translate.Message  // previous_response_id 命中的旧历史
	input  []translate.Message  // 本轮请求的 input（未合并前的）
}
