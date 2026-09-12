package gateway

import (
	"bytes"
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

// dispatch 按候选链依次尝试调用上游。直连路由是单候选的特例；别名路由可含
// 多个候选，调用失败（网络错误 / 上游错误状态）自动故障转移到下一个候选。
// 每个候选的成败都各自记一条 usage（PG 下各归档一条对话记录）。
func (g *Gateway) dispatch(w http.ResponseWriter, r *http.Request, inFormat string, key *model.ExtKey, targets []model.AliasTarget, body []byte) {
	first := targets[0]
	logger.Info("completion request",
		"key_id", key.ID,
		"key_name", key.Name,
		"upstream", first.Upstream.Name,
		"upstream_format", first.Upstream.Format,
		"model", first.ModelName,
		"candidates", len(targets),
		"in_format", inFormat,
		"stream", bodyHasStream(body),
	)
	irReq, err := decodeInbound(body, inFormat)
	if err != nil {
		WriteError(w, 400, inFormat, "failed to decode request: "+err.Error(), "invalid_request_error")
		g.recordUsage(key, first.Upstream, first.ModelName, inFormat, translate.Usage{}, false, 0, "error")
		return
	}
	irReq.Model = first.ModelName

	// 对话归档（仅 PG）：在 responses session 合并 irReq 之前快照请求 IR，
	// 各次候选调用的 newConvCtx 共用这份快照。
	reqIRJSON := g.snapshotRequestIR(irReq)

	var sess *sessionCtx
	if inFormat == "responses" {
		sess = &sessionCtx{respID: responses.NewID(), input: irReq.Messages}
		if pid, _ := irReq.Extra["previous_response_id"].(string); pid != "" {
			hist, ok, err := g.sessions.Get(pid)
			if err != nil {
				WriteError(w, 500, inFormat, "session lookup failed: "+err.Error(), "internal_error")
				g.recordUsage(key, first.Upstream, first.ModelName, inFormat, translate.Usage{}, false, 0, "error")
				g.newConvCtx(r, key, first.Upstream, first.ModelName, inFormat, reqIRJSON, irReq.Stream, body).finish("error", translate.Usage{}, nil)
				return
			}
			if !ok {
				WriteError(w, 400, inFormat, "unknown previous_response_id: "+pid, "invalid_previous_response_id")
				g.recordUsage(key, first.Upstream, first.ModelName, inFormat, translate.Usage{}, false, 0, "error")
				g.newConvCtx(r, key, first.Upstream, first.ModelName, inFormat, reqIRJSON, irReq.Stream, body).finish("error", translate.Usage{}, nil)
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
		g.handleStream(w, r, inFormat, key, targets, irReq, reqIRJSON, body, sess)
		return
	}

	// 非流式：按序尝试，任一候选成功即返回；全部失败回最后一个错误。
	var lastErr error
	for i := range targets {
		t := &targets[i]
		irReq.Model = t.ModelName
		rec := g.newConvCtx(r, key, t.Upstream, t.ModelName, inFormat, reqIRJSON, false, body)
		callStart := time.Now()
		result, err := g.client.Call(r.Context(), t.Upstream, irReq, r.Header)
		callDur := time.Since(callStart)
		if err != nil {
			if len(targets) > 1 {
				logger.Warn("candidate call failed, failing over", "alias_candidate", i, "upstream", t.Upstream.Name, "model", t.ModelName, "err", err)
			}
			g.recordUsage(key, t.Upstream, t.ModelName, inFormat, translate.Usage{}, false, callDur, "error")
			rec.finish("error", translate.Usage{}, nil)
			lastErr = err
			continue
		}
		if sess != nil {
			result.Response.ID = sess.respID
		}
		g.handleNonStream(w, inFormat, result, key, t.Upstream, t.ModelName, irReq.Stream, sess, rec, callDur)
		return
	}
	if ue, ok := lastErr.(*upstream.UpstreamError); ok {
		WriteError(w, ue.StatusCode, inFormat, ue.Message(), mapErrorType(inFormat, ue.StatusCode, ue.ErrorType()))
	} else {
		WriteError(w, 502, inFormat, "upstream call failed: "+lastErr.Error(), "upstream_error")
	}
}

func bodyHasStream(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.Stream
}

func (g *Gateway) handleNonStream(w http.ResponseWriter, inFormat string, result *upstream.Result, key *model.ExtKey, u *model.Upstream, realModel string, stream bool, sess *sessionCtx, rec *convCtx, callDur time.Duration) {
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
		g.recordUsage(key, u, realModel, inFormat, result.Response.Usage, false, callDur, "error")
		rec.finish("error", result.Response.Usage, result.Response)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
	if sess != nil {
		g.saveSession(sess, result.Response.Content)
	}
	usage := result.Usage()
	g.recordUsage(key, u, realModel, inFormat, usage, false, callDur, "ok")
	// 非流式：发给客户端的原始字节就是 out。
	if rec != nil {
		rec.tee = &teeWriter{buf: bytes.NewBuffer(out)}
	}
	rec.finish("ok", usage, result.Response)
	logger.Info("completion done",
		"upstream", u.Name,
		"model", realModel,
		"stream", false,
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"duration_ms", callDur.Milliseconds(),
		"status", "ok",
	)
}

// callWithKeepalive 在流式头部已 flush 后执行一次上游调用；等待期间按
// keepalive ticker 向客户端发 ping。clientGone=true 表示客户端上下文先结束
// （上游调用随 r.Context() 取消，带缓冲的 callCh 保证 goroutine 不泄漏）。
func (g *Gateway) callWithKeepalive(r *http.Request, keepalive *time.Ticker, writePing func(), u *model.Upstream, irReq *translate.Request, streamStart time.Time) (result *upstream.Result, err error, clientGone bool) {
	type callRet struct {
		result *upstream.Result
		err    error
	}
	callCh := make(chan callRet, 1)
	go func() {
		res, err := g.client.Call(r.Context(), u, irReq, r.Header)
		callCh <- callRet{res, err}
	}()
	for {
		select {
		case ret := <-callCh:
			if ret.err != nil {
				logger.Warn("upstream call returned with error", "elapsed_ms", time.Since(streamStart).Milliseconds(), "err", ret.err)
			} else {
				logger.Info("call returned", "elapsed_ms", time.Since(streamStart).Milliseconds())
			}
			return ret.result, ret.err, false
		case <-keepalive.C:
			writePing()
		case <-r.Context().Done():
			logger.Info("client context done (before call returned)", "elapsed_ms", time.Since(streamStart).Milliseconds())
			return nil, nil, true
		}
	}
}

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request, inFormat string, key *model.ExtKey, targets []model.AliasTarget, irReq *translate.Request, reqIRJSON []byte, body []byte, sess *sessionCtx) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, 500, inFormat, "streaming not supported", "internal_error")
		t := targets[0]
		g.recordUsage(key, t.Upstream, t.ModelName, inFormat, translate.Usage{}, true, 0, "error")
		g.newConvCtx(r, key, t.Upstream, t.ModelName, inFormat, reqIRJSON, true, body).finish("error", translate.Usage{}, nil)
		return
	}
	// 对话归档：flusher 断言之后安装 tee，捕获发给客户端的全部字节。
	// 多候选共享一个 tee（keep-alive 与后续帧都在同一缓冲），各次尝试创建的
	// rec 引用它。
	var tee *teeWriter
	if reqIRJSON != nil {
		tee = newTeeWriter(w, convRawCap)
		w = tee
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	flusher.Flush()
	streamStart := time.Now()
	logger.Info("stream header flushed", "upstream", targets[0].Upstream.Name, "model", targets[0].ModelName, "candidates", len(targets))

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

	keepalive := time.NewTicker(500 * time.Millisecond)
	defer keepalive.Stop()

	// 第一阶段：按序尝试候选，直到某次调用成功建立。期间客户端只看到
	// keep-alive 延续；某候选调用失败后转移到下一个候选对客户端透明。
	// 一旦进入事件转发阶段（有内容帧流出）就不再转移。
	var result *upstream.Result
	var win *model.AliasTarget
	var winStart time.Time // 命中候选的调用开始时刻，作为该次调用的计时起点
	var rec *convCtx
	var lastErr error
	for i := range targets {
		t := &targets[i]
		irReq.Model = t.ModelName
		rec = g.newConvCtx(r, key, t.Upstream, t.ModelName, inFormat, reqIRJSON, true, body)
		if rec != nil {
			rec.tee = tee
		}
		callStart := time.Now()
		res, err, clientGone := g.callWithKeepalive(r, keepalive, writePing, t.Upstream, irReq, streamStart)
		callDur := time.Since(callStart)
		if clientGone {
			g.recordUsage(key, t.Upstream, t.ModelName, inFormat, translate.Usage{}, true, callDur, "error")
			rec.finish("error", translate.Usage{}, nil)
			logger.Info("completion done",
				"upstream", t.Upstream.Name, "model", t.ModelName, "stream", true,
				"input_tokens", 0, "output_tokens", 0, "status", "error", "reason", "client_gone_before_call_done",
			)
			return
		}
		if err != nil {
			lastErr = err
			if len(targets) > 1 {
				logger.Warn("stream candidate call failed, failing over", "alias_candidate", i, "upstream", t.Upstream.Name, "model", t.ModelName, "err", err)
			}
			g.recordUsage(key, t.Upstream, t.ModelName, inFormat, translate.Usage{}, true, callDur, "error")
			rec.finish("error", translate.Usage{}, nil)
			continue
		}
		result, win, winStart = res, t, callStart
		break
	}

	if result == nil {
		// 全部候选失败：头部已 200，只能写带内错误帧。
		var msg, errType string
		var status int
		if ue, ok := lastErr.(*upstream.UpstreamError); ok {
			msg, errType, status = ue.Message(), mapErrorType(inFormat, ue.StatusCode, ue.ErrorType()), ue.StatusCode
		} else {
			msg, errType, status = "upstream call failed: "+lastErr.Error(), "upstream_error", 502
		}
		logger.Error("upstream call failed after stream header sent",
			"upstream", targets[len(targets)-1].Upstream.Name, "model", targets[len(targets)-1].ModelName, "status", status, "err", msg, "in_format", inFormat)
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
		logger.Info("completion done",
			"upstream", targets[len(targets)-1].Upstream.Name, "model", targets[len(targets)-1].ModelName, "stream", true,
			"input_tokens", 0, "output_tokens", 0, "status", "error",
		)
		return
	}

	u := win.Upstream
	realModel := win.ModelName

	// 命中候选确定后再建编码器（此前只写过 keep-alive，无内容帧）。
	var encoder interface {
		Encode(evt *translate.StreamEvent) ([][]byte, error)
	}
	switch inFormat {
	case "anthropic":
		// Stateful encoder: rewrites content_block indices to a 0-based
		// contiguous sequence (the spec requires it; an OpenAI-only-tool-call
		// upstream starts at index 1).
		encoder = anthropic.NewStreamEncoder()
	case "responses":
		encoder = responses.NewStreamEncoder(realModel, sess.respID)
	default:
		encoder = openai.NewStreamEncoder(realModel)
	}

	if result.Response != nil {
		// 非流式 JSON 应答：客户端拿到的响应 id 必须与会话 key 一致，
		// 否则后续 previous_response_id 续接会 400。
		if sess != nil {
			result.Response.ID = sess.respID
		}
		var out []byte
		var encErr error
		switch inFormat {
		case "anthropic":
			out, encErr = anthropic.EncodeResponse(result.Response)
		case "responses":
			out, encErr = responses.EncodeResponse(result.Response)
		default:
			out, encErr = openai.EncodeResponse(result.Response)
		}
		if encErr != nil {
			logger.Error("stream non-stream response encode failed", "in_format", inFormat, "err", encErr)
			g.recordUsage(key, u, realModel, inFormat, result.Response.Usage, true, time.Since(winStart), "error")
			rec.finish("error", result.Response.Usage, result.Response)
			return
		}
		w.Write(out)
		flusher.Flush()
		usage := result.Usage()
		g.recordUsage(key, u, realModel, inFormat, usage, true, time.Since(winStart), "ok")
		rec.finish("ok", usage, result.Response)
		logger.Info("completion done",
			"upstream", u.Name, "model", realModel, "stream", true,
			"input_tokens", usage.InputTokens, "output_tokens", usage.OutputTokens, "status", "ok",
		)
		// 上游用非流式 JSON 应答流式请求：输出完整，照常累积会话
		if sess != nil {
			g.saveSession(sess, result.Response.Content)
		}
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
			// 对话归档：累积原始 IR 事件（不喂下面合成的 content_block_start，
			// streamRecorder 的 ensureKind 已对缺失 start 做惰性开块）。
			if rec != nil {
				rec.acc.Add(ev)
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
				if frames, e := encoder.Encode(synthEv); e == nil {
					for _, f := range frames {
						w.Write(f)
					}
					logger.Info("synthesized content_block_start", "index", ev.Index, "block_type", blockType)
				}
				blockStarted[ev.Index] = true
			}
			if ev.Type == "content_block_start" {
				blockStarted[ev.Index] = true
			}
			frames, err := encoder.Encode(ev)
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
			// 只有调用成功后保存：上游流中途出错时 respID 已随 response.created
			// 发给客户端，若把部分输出并入历史，客户端带同一 id 重试会重复内容。
			if sess != nil && result.StreamErr() == nil {
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
	callDur := time.Since(winStart)
	g.recordUsage(key, u, realModel, inFormat, usage, true, callDur, status)
	// 对话归档：用流累积器还原完整响应（含思维链真签名、工具调用），
	// clientGonePost / StreamErr 时部分对话以 error 状态如实记录。
	if rec != nil {
		id := rec.acc.msgID
		if sess != nil {
			id = sess.respID
		}
		rec.finish(status, usage, &translate.Response{
			ID:         id,
			Model:      realModel,
			Content:    rec.acc.Content(),
			StopReason: rec.acc.stopReason,
			Usage:      usage,
		})
	}
	logger.Info("completion done",
		"upstream", u.Name,
		"model", realModel,
		"stream", true,
		"input_tokens", usage.InputTokens,
		"output_tokens", usage.OutputTokens,
		"duration_ms", callDur.Milliseconds(),
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

func (g *Gateway) recordUsage(key *model.ExtKey, u *model.Upstream, realModel, inFormat string, usage translate.Usage, stream bool, dur time.Duration, status string) {
	total := usage.InputTokens + usage.OutputTokens
	rec := &model.UsageRecord{
		UpstreamName:        u.Name,
		Model:               realModel,
		InFormat:            inFormat,
		UpFormat:            u.Format,
		PromptTokens:        usage.InputTokens,
		CompletionTokens:    usage.OutputTokens,
		TotalTokens:         total,
		CacheReadTokens:     usage.CacheReadTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		ReasoningTokens:     usage.ReasoningTokens,
		DurationMs:          dur.Milliseconds(),
		Stream:              stream,
		Status:              status,
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
	respID string              // 返回给客户端的响应 id，也是会话 key
	prev   []translate.Message // previous_response_id 命中的旧历史
	input  []translate.Message // 本轮请求的 input（未合并前的）
}
