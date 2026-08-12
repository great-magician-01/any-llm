package gateway

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
	"github.com/great-magician-01/any-llm/internal/translate"
)

// convRawCap 限制单条流响应捕获的字节上限，避免个别超大对话撑爆
// 512 缓冲的 writer channel 内存。
const convRawCap = 64 << 20 // 64 MiB

// convReqRawCap 限制归档的请求体原始字节上限。请求体本身没有大小限制
// （io.ReadAll 整包读入），归档闭包又会在 writer 队列里再持有它一份，
// 只保留前缀即可满足回放定位用途。
const convReqRawCap = 1 << 20 // 1 MiB

// convCtx 贯穿单个请求的对话记录上下文。SQLite 时恒为 nil（记录关闭），
// 所有 finish 调用对 nil 接收者直接返回，零开销。
type convCtx struct {
	g            *Gateway
	createdAt    time.Time
	extKeyID     *int64
	upstreamID   *int64
	upstreamName string
	model        string
	inFormat     string
	upFormat     string
	harness      string
	userAgent    string
	stream       bool
	reqIRJSON    []byte          // 请求 IR 的 JSON，创建时快照（session 合并前）
	reqRaw       []byte          // 入站请求体（别名，不复制）
	acc          *streamRecorder // 流请求时非 nil
	tee          *teeWriter      // 流请求安装后非 nil
}

// newConvCtx 在 PG 上为请求创建记录上下文；非 PG（或 marshal 失败）返回 nil。
// 必须在 dispatch 里 irReq.Model = realModel 之后、responses session 合并
// irReq 之前调用，保证 request_ir 是客户端原始请求的归一化快照。
func (g *Gateway) newConvCtx(r *http.Request, key *model.ExtKey, u *model.Upstream, realModel, inFormat string, irReq *translate.Request, body []byte) *convCtx {
	if db.DialectOf(g.db) != db.DialectPostgres {
		return nil
	}
	reqIRJSON, err := json.Marshal(irReq)
	if err != nil {
		logger.Warn("conversation: request IR marshal failed, skipping record", "err", err)
		return nil
	}
	ua := r.Header.Get("User-Agent")
	c := &convCtx{
		g:         g,
		createdAt: time.Now(),
		model:     realModel,
		inFormat:  inFormat,
		harness:   detectHarness(ua),
		userAgent: ua,
		stream:    irReq.Stream,
		reqIRJSON: reqIRJSON,
		reqRaw:    body,
	}
	if key != nil {
		kid := key.ID
		c.extKeyID = &kid
	}
	if u != nil {
		uid := u.ID
		c.upstreamID = &uid
		c.upstreamName = u.Name
		c.upFormat = u.Format
	}
	if irReq.Stream {
		c.acc = newStreamRecorder()
	}
	return c
}

// finish 组装并 enqueue 一条对话记录。每个请求恰好调用一次；nil 接收者直接返回。
// respIR 为 nil（如上游错误）时 response_ir 记为 "{}"。
func (c *convCtx) finish(status string, usage translate.Usage, respIR *translate.Response) {
	if c == nil {
		return
	}
	respJSON := []byte("{}")
	if respIR != nil {
		if b, err := json.Marshal(respIR); err == nil {
			respJSON = b
		} else {
			logger.Warn("conversation: response IR marshal failed, storing empty", "err", err)
		}
	}
	var respRaw []byte
	if c.tee != nil {
		respRaw = c.tee.buf.Bytes()
	}
	// 错误路径（上游报错/编码失败/客户端断连）tee 未安装或缓冲为空时
	// Bytes() 返回 nil，插入会变成 NULL 违反 NOT NULL 约束导致整条记录
	// 丢弃——统一兜底为空字节。
	if respRaw == nil {
		respRaw = []byte{}
	}
	// 请求体只保留前缀（拷贝，避免闭包持有整包底层数组）。
	reqRaw := c.reqRaw
	if len(reqRaw) > convReqRawCap {
		reqRaw = reqRaw[:convReqRawCap]
	}
	reqRaw = append([]byte(nil), reqRaw...)
	rec := &model.ConversationRecord{
		ExtKeyID:            c.extKeyID,
		UpstreamID:          c.upstreamID,
		UpstreamName:        c.upstreamName,
		Model:               c.model,
		InFormat:            c.inFormat,
		UpFormat:            c.upFormat,
		Harness:             c.harness,
		UserAgent:           c.userAgent,
		Stream:              c.stream,
		Status:              status,
		PromptTokens:        usage.InputTokens,
		CompletionTokens:    usage.OutputTokens,
		TotalTokens:         usage.InputTokens + usage.OutputTokens,
		CacheReadTokens:     usage.CacheReadTokens,
		CacheCreationTokens: usage.CacheCreationTokens,
		ReasoningTokens:     usage.ReasoningTokens,
		RequestIR:           string(c.reqIRJSON),
		ResponseIR:          string(respJSON),
		RequestRaw:          reqRaw,
		ResponseRaw:         respRaw,
		CreatedAt:           c.createdAt,
	}
	if c.g.writer != nil {
		c.g.writer.DoAsync(func(d *sql.DB) error { return model.InsertConversation(d, rec) })
	} else {
		if err := model.InsertConversation(c.g.db, rec); err != nil {
			logger.Error("conversation: sync write failed", "model", rec.Model, "err", err)
		}
	}
}

// teeWriter 记录写给客户端的每个字节，同时原样透传。嵌入 http.ResponseWriter
// 并显式实现 Flush，因此 *teeWriter 始终满足 http.Flusher。
type teeWriter struct {
	http.ResponseWriter
	buf *bytes.Buffer
	max int
}

func newTeeWriter(w http.ResponseWriter, max int) *teeWriter {
	return &teeWriter{ResponseWriter: w, buf: new(bytes.Buffer), max: max}
}

// Write 先把（截断到剩余额度的）字节入缓冲再透传：即便底层写失败（如客户端
// 断连），已尝试发送的字节也被记录，部分对话得以保留。
func (t *teeWriter) Write(p []byte) (int, error) {
	if rem := t.max - t.buf.Len(); rem > 0 {
		if len(p) > rem {
			t.buf.Write(p[:rem])
		} else {
			t.buf.Write(p)
		}
	}
	return t.ResponseWriter.Write(p)
}

func (t *teeWriter) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
