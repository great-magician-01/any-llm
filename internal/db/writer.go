package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/great-magician-01/any-llm/internal/logger"
)

var ErrWriterStopped = errors.New("writer is stopped")

type WriteFunc func(*sql.DB) error

type Writer struct {
	DB       *sql.DB
	ch       chan writeReq
	stopCh   chan struct{}
	closed   bool
	mu       sync.Mutex
	wg       sync.WaitGroup
	inflight sync.WaitGroup
}

type writeReq struct {
	fn     WriteFunc
	result chan error
}

func NewWriter(d *sql.DB, bufSize int) *Writer {
	return &Writer{
		DB:     d,
		ch:     make(chan writeReq, bufSize),
		stopCh: make(chan struct{}),
	}
}

func (w *Writer) Start() {
	w.wg.Add(1)
	go w.loop()
}

func (w *Writer) loop() {
	defer w.wg.Done()
	for {
		select {
		case req := <-w.ch:
			err := req.fn(w.DB)
			if req.result != nil {
				req.result <- err
			} else {
				logger.Error("async writer: write failed", "err", err)
			}
		case <-w.stopCh:
			for {
				select {
				case req := <-w.ch:
					err := req.fn(w.DB)
					if req.result != nil {
						req.result <- err
					} else {
						logger.Error("async writer (drain): write failed", "err", err)
					}
				default:
					return
				}
			}
		}
	}
}

func (w *Writer) Stop() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	w.mu.Unlock()
	w.inflight.Wait()
	close(w.stopCh)
	w.wg.Wait()
}

func (w *Writer) DoAsync(fn WriteFunc) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()
	select {
	case w.ch <- writeReq{fn: fn}:
	default:
		logger.Warn("async writer: channel full, write dropped")
	}
}

func (w *Writer) DoSync(fn WriteFunc) error {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return fmt.Errorf("%w", ErrWriterStopped)
	}
	w.inflight.Add(1)
	w.mu.Unlock()
	defer w.inflight.Done()

	result := make(chan error, 1)
	w.ch <- writeReq{fn: fn, result: result}
	return <-result
}
