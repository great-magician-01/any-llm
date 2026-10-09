package gateway

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/store"
	"github.com/great-magician-01/any-llm/internal/translate"
	"github.com/great-magician-01/any-llm/internal/translate/anthropic"
	"github.com/great-magician-01/any-llm/internal/translate/openai"
	"github.com/great-magician-01/any-llm/internal/translate/responses"
	"github.com/great-magician-01/any-llm/internal/upstream"
)

// concurrencyLimitMessage 是全部候选都因上游并发上限被跳过时的 429 文案，
// 非流式与流式两条路径共用（SDK 会据此自动重试）。
const concurrencyLimitMessage = "upstream concurrency limit reached, please retry later"

// logConcurrencySkip 记录一次因上游并发上限被跳过的候选：非流式失败转移、
// 流式预占、流式失败转移三处共用同一组日志字段。
func logConcurrencySkip(msg string, i int, t *store.AliasTarget) {
	logger.Info(msg, "alias_candidate", i, "upstream", t.Upstream.Name, "model", t.ModelName, "max_concurrent", t.Upstream.MaxConcurrent)
}

// dispatch 按候选链依次尝试调用上游。直连路由是单候选的特例；别名路由可含
// 多个候选，调用失败（网络错误 / 上游错误状态）自动故障转移到下一个候选。
// 每个候选的成败都各自记一条 usage（PG 下各归档一条对话记录）。
//
// 多格式上游：每个候选先经 effectiveUpstream 按入站端点格式解析实际使用的
// 端点——命中附加端点则原生直通（零转译），否则用主格式走 IR 转译（与单格式
// 上游行为一致）。usage / 对话归档的 up_format 记实际生效的格式。
func (g *Gateway) dispatch(w http.ResponseWriter, r *http.Request, inFormat string, key *store.ExtKey, targets []store.AliasTarget, body []byte) {
	first := targets[0]
	logger.Info("completion request",
		"key_id", key.ID,
		"key_label", key.Label,
		"upstream", first.Upstream.Name,
		"upstream_format", first.Upstream.Format,
		"effective_format", effectiveUpstream(first.Upstream, inFormat).Format,
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
	var prevRespID string
	if inFormat == "responses" {
		sess = &sessionCtx{respID: responses.NewID(), input: irReq.Messages}
		if pid, _ := irReq.Extra["previous_response_id"].(string); pid != "" {
			prevRespID = pid
			hist, ok, err := g.sessions.Get(pid)
			if err != nil {
				WriteError(w, 500, inFormat, "session lookup failed: "+err.Error(), "internal_error")
				g.recordUsage(key, first.Upstream, first.ModelName, inFormat, translate.Usage{}, false, 0, "error")
				g.newConvCtx(r, key, first.Upstream, first.ModelName, inFormat, reqIRJSON, irReq.Stream, body, convInfo{}).finish("error", translate.Usage{}, nil)
				return
			}
			if !ok {
				WriteError(w, 400, inFormat, "unknown previous_response_id: "+pid, "invalid_previous_response_id")
				g.recordUsage(key, first.Upstream, first.ModelName, inFormat, translate.Usage{}, false, 0, "error")
				g.newConvCtx(r, key, first.Upstream, first.ModelName, inFormat, reqIRJSON, irReq.Stream, body, convInfo{}).finish("error", translate.Usage{}, nil)
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

	// 会话聚合视图：从请求头 / metadata / responses 链提取会话 id。
	// 全部未命中（zero）时写入侧零开销，行为与现状一致。
	conv := extractConversationID(r, irReq, prevRespID, sess)

	if irReq.Stream {
		g.handleStream(w, r, inFormat, key, targets, irReq, reqIRJSON, body, sess, conv)
		return
	}

	// 非流式：按序尝试，任一候选成功即返回；全部失败回最后一个错误。
	var lastErr error
	busy := 0
	for i := range targets {
		t := &targets[i]
		release, ok := g.conc.tryAcquire(t.Upstream)
		if !ok {
			// 并发已达上限的候选直接跳过（未发起上游调用，不记 usage），
			// 故障转移到下一候选；全部候选都满则在循环后统一回 429。
			busy++
			logConcurrencySkip("candidate skipped: upstream concurrency limit reached", i, t)
			continue
		}
		irReq.Model = t.ModelName
		eu := effectiveUpstream(t.Upstream, inFormat)
		rec := g.newConvCtx(r, key, eu, t.ModelName, inFormat, reqIRJSON, false, body, conv)
		callStart := time.Now()
		result, err := g.client.Call(r.Context(), eu, irReq, r.Header)
		// 非流式 Call 返回时上游响应体已完整读取，立即释放并发槽。
		release()
		callDur := time.Since(callStart)
		if err != nil {
			if len(targets) > 1 {
				logger.Warn("candidate call failed, failing over", "alias_candidate", i, "upstream", t.Upstream.Name, "model", t.ModelName, "err", err)
			}
			g.recordUsage(key, eu, t.ModelName, inFormat, translate.Usage{}, false, callDur, "error")
			rec.finish("error", translate.Usage{}, nil)
			lastErr = err
			continue
		}
		if sess != nil {
			result.Response.ID = sess.respID
		}
		g.handleNonStream(w, inFormat, result, key, eu, t.ModelName, sess, rec, callDur)
		return
	}
	if busy == len(targets) {
		// 所有候选都因并发上限被跳过：没有真实上游错误可回，回 429 让客户端重试。
		WriteError(w, 429, inFormat, concurrencyLimitMessage, "rate_limit_error")
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

func (g *Gateway) handleNonStream(w http.ResponseWriter, inFormat string, result *upstream.Result, key *store.ExtKey, u *store.Upstream, realModel string, sess *sessionCtx, rec *convCtx, callDur time.Duration) {
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
func (g *Gateway) callWithKeepalive(r *http.Request, keepalive *time.Ticker, writePing func(), u *store.Upstream, irReq *translate.Request, streamStart time.Time) (result *upstream.Result, err error, clientGone bool) {
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

func (g *Gateway) handleStream(w http.ResponseWriter, r *http.Request, inFormat string, key *store.ExtKey, targets []store.AliasTarget, irReq *translate.Request, reqIRJSON []byte, body []byte, sess *sessionCtx, conv convInfo) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, 500, inFormat, "streaming not supported", "internal_error")
		t := targets[0]
		g.recordUsage(key, t.Upstream, t.ModelName, inFormat, translate.Usage{}, true, 0, "error")
		g.newConvCtx(r, key, t.Upstream, t.ModelName, inFormat, reqIRJSON, true, body, conv).finish("error", translate.Usage{}, nil)
		return
	}
	// 并发上限：在 flush 200 头部之前先为候选占槽——所有候选都满还能回干净的
	// 429（SDK 会自动重试）；一旦头部流出就只能写带内错误帧了。占到的槽位
	// 供下面的候选循环直接使用。
	heldIdx := -1
	var heldRelease func()
	for i := range targets {
		if rel, ok := g.conc.tryAcquire(targets[i].Upstream); ok {
			heldIdx, heldRelease = i, rel
			break
		}
		logConcurrencySkip("stream candidate skipped: upstream concurrency limit reached", i, &targets[i])
	}
	if heldRelease == nil {
		WriteError(w, 429, inFormat, concurrencyLimitMessage, "rate_limit_error")
		return
	}
	// 命中候选的并发槽持有到本函数结束（流式期间上游连接一直存活）。
	// 释放函数幂等：失败路径上提前 release 后，defer 的再调用是空操作。
	winRelease := heldRelease
	defer func() { winRelease() }()

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
	w.WriteHeader(http.StatusOK)
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

	// 第一阶段：从预占成功的候选开始按序尝试，直到某次调用成功建立。期间
	// 客户端只看到 keep-alive 延续；某候选调用失败后转移到下一个候选对客户端
	// 透明。一旦进入事件转发阶段（有内容帧流出）就不再转移。并发已满的候选
	// 直接跳过（预占时已过滤一遍；循环内再试占是为了覆盖故障转移到的候选）。
	var result *upstream.Result
	var win *store.AliasTarget
	var winEff *store.Upstream // 命中候选按入站格式解析后的生效端点（usage/错误透传用）
	var winStart time.Time     // 命中候选的调用开始时刻，作为该次调用的计时起点
	var rec *convCtx
	var lastErr error
	for i := heldIdx; i < len(targets); i++ {
		t := &targets[i]
		var release func()
		if i == heldIdx {
			release = heldRelease
		} else {
			rel, ok := g.conc.tryAcquire(t.Upstream)
			if !ok {
				logConcurrencySkip("stream candidate skipped: upstream concurrency limit reached", i, t)
				continue
			}
			release = rel
		}
		winRelease = release
		irReq.Model = t.ModelName
		eu := effectiveUpstream(t.Upstream, inFormat)
		rec = g.newConvCtx(r, key, eu, t.ModelName, inFormat, reqIRJSON, true, body, conv)
		if rec != nil {
			rec.tee = tee
		}
		callStart := time.Now()
		res, err, clientGone := g.callWithKeepalive(r, keepalive, writePing, eu, irReq, streamStart)
		callDur := time.Since(callStart)
		if clientGone {
			release()
			g.recordUsage(key, eu, t.ModelName, inFormat, translate.Usage{}, true, callDur, "error")
			rec.finish("error", translate.Usage{}, nil)
			logger.Info("completion done",
				"upstream", t.Upstream.Name, "model", t.ModelName, "stream", true,
				"input_tokens", 0, "output_tokens", 0, "status", "error", "reason", "client_gone_before_call_done",
			)
			return
		}
		if err != nil {
			release()
			lastErr = err
			if len(targets) > 1 {
				logger.Warn("stream candidate call failed, failing over", "alias_candidate", i, "upstream", t.Upstream.Name, "model", t.ModelName, "err", err)
			}
			g.recordUsage(key, eu, t.ModelName, inFormat, translate.Usage{}, true, callDur, "error")
			rec.finish("error", translate.Usage{}, nil)
			continue
		}
		result, win, winEff, winStart = res, t, eu, callStart
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
		writeStreamErrorFrame(w, inFormat, msg, errType)
		flusher.Flush()
		logger.Info("completion done",
			"upstream", targets[len(targets)-1].Upstream.Name, "model", targets[len(targets)-1].ModelName, "stream", true,
			"input_tokens", 0, "output_tokens", 0, "status", "error",
		)
		return
	}

	// 命中候选的生效端点：原生直通时 u.Format 已是附加端点的格式，下面
	// recordUsage 的 up_format 与错误类型透传判断都按实际使用的格式记。
	u := winEff
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
		// 上游无视 stream:true、直接回了非流式 JSON：把完整响应展开成 IR 流事件
		// 再走同一条流式编码路径，客户端拿到的仍是**合法的 SSE 流**（带真实内容与
		// usage）。直接写裸 JSON 不行——严格 SDK 只认 data:/event: 帧，裸 JSON 会被
		// 当成无法解析的一行丢掉，客户端最终什么都拿不到。
		// 客户端看到的响应 id 必须与会话 key 一致，否则 previous_response_id 续接会 400。
		if sess != nil {
			result.Response.ID = sess.respID
		}
		for _, ev := range translate.ResponseStreamEvents(result.Response) {
			frames, err := encoder.Encode(ev)
			if err != nil {
				logger.Warn("stream non-stream response encode skipped", "in_format", inFormat, "type", ev.Type, "err", err)
				continue
			}
			for _, f := range frames {
				w.Write(f)
			}
		}
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
	// upstreamSignalledErr 表示上游在流中间发过 error 事件（Anthropic 的
	// event: error / Responses 的 response.failed）：已经给客户端写过带内错误帧，
	// 此后不能再补结束帧，usage 必须记 error。
	upstreamSignalledErr := false
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
			// 上游在流中间报错：三个出站编码器都没有 error 分支——openai /
			// responses 出站会把这个事件整个丢掉，anthropic 出站只会发出一个没有
			// error 明细的空壳帧；两条路都让客户端拿不到可用的错误信息与结束信号，
			// usage 还会被记成 ok。这里复用「全部候选失败」的带内错误帧，按客户端
			// 格式补一帧并结束本请求。
			if ev.Type == "error" {
				// 透传上游的错误细节（同格式下类型原样保留，如 overloaded_error）；
				// 细节缺失或跨格式时回退通用值——客户端拿到的至少是一句人话，
				// 而不是无法诊断的常量。
				msg := ev.ErrMessage
				if msg == "" {
					msg = "upstream stream error"
				}
				errType := "upstream_error"
				if ev.ErrType != "" && u.Format == inFormat {
					errType = ev.ErrType
				}
				logger.Warn("upstream stream error event, ending stream",
					"upstream", u.Name, "model", realModel, "in_format", inFormat,
					"upstream_error_type", ev.ErrType, "upstream_error_message", ev.ErrMessage,
					"elapsed_ms", time.Since(streamStart).Milliseconds())
				writeStreamErrorFrame(w, inFormat, msg, errType)
				flusher.Flush()
				upstreamSignalledErr = true
				goto done
			}
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
	// 成功后把累积输出写入会话存储。上游已发 error 事件时跳过 Flush：一次失败的
	// 流不能被补成「正常完成」。
	if !clientGonePost && !upstreamSignalledErr {
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
	if clientGonePost || upstreamSignalledErr {
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

// writeStreamErrorFrame 在 200 头已经 flush 之后，按客户端格式写一个「带内」
// 错误帧（此时不可能再回 HTTP 错误码）。openai 出站用 data: {"error":{...}}；
// anthropic 出站用规范的嵌套 error 对象；responses 出站用该协议规范的扁平
// code/message（嵌套的 Anthropic 形状会让 responses SDK 解析不出错误明细）。
// 不写任何结束帧：失败的流不能带完成标记（message_stop / [DONE] /
// response.completed 都会让客户端误判为正常完成）。
// 两条触发路径共用：全部候选失败，以及上游在流中间发 error 事件。
func writeStreamErrorFrame(w http.ResponseWriter, inFormat, message, errType string) {
	switch inFormat {
	case "responses":
		payload, _ := json.Marshal(map[string]any{
			"type": "error", "code": errType, "message": message,
		})
		w.Write([]byte("event: error\ndata: " + string(payload) + "\n\n"))
	case "anthropic":
		payload, _ := json.Marshal(map[string]any{
			"type":  "error",
			"error": map[string]any{"type": errType, "message": message},
		})
		w.Write([]byte("event: error\ndata: " + string(payload) + "\n\n"))
	default:
		payload, _ := json.Marshal(map[string]any{
			"error": map[string]any{"message": message, "type": errType},
		})
		w.Write([]byte("data: " + string(payload) + "\n\n"))
	}
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

// effectiveUpstream 按入站端点格式解析本次请求对候选上游实际使用的端点：
// 附加端点命中 inFormat → 浅拷贝换上该条目的 BaseURL/Format（原生直通，
// 零转译）；否则原样返回（主格式兜底，走 IR 转译，与单格式上游行为一致）。
// 浅拷贝安全：只换两个 string 字段，ExtraEndpoints 等 slice 字段不改动。
// 上游调用层（upstream.Client.Call 的编码/路径/认证头/流式解码）只对
// 生效视图的 Format 分发，因此本身无需感知多端点。
func effectiveUpstream(u *store.Upstream, inFormat string) *store.Upstream {
	for _, ep := range u.ExtraEndpoints {
		if ep.Format == inFormat {
			c := *u
			c.Format = ep.Format
			c.BaseURL = ep.BaseURL
			return &c
		}
	}
	return u
}

func (g *Gateway) recordUsage(key *store.ExtKey, u *store.Upstream, realModel, inFormat string, usage translate.Usage, stream bool, dur time.Duration, status string) {
	total := usage.InputTokens + usage.OutputTokens
	rec := &store.UsageRecord{
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
	// key 与 u 的所有调用点都保证非 nil（鉴权失败早已返回），这里直接取值；
	// 用局部变量而非 &key.ID，避免异步写入期间与调用方共享同一个结构体字段。
	kid := key.ID
	rec.ExtKeyID = &kid
	uid := u.ID
	rec.UpstreamID = &uid
	if g.writer != nil {
		g.writer.DoAsync(func(d *sql.DB) error { return store.InsertUsage(d, rec) })
	} else {
		if err := store.InsertUsage(g.db, rec); err != nil {
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
