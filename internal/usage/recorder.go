package usage

import (
	"database/sql"
	"sync"

	"github.com/great-magician-01/any-llm/internal/model"
)

type Recorder struct {
	db   *sql.DB
	ch   chan *model.UsageRecord
	done chan struct{}
	wg   sync.WaitGroup
}

func NewRecorder(db *sql.DB, bufferSize int) *Recorder {
	return &Recorder{
		db:   db,
		ch:   make(chan *model.UsageRecord, bufferSize),
		done: make(chan struct{}),
	}
}

func (r *Recorder) Start() {
	r.wg.Add(1)
	go r.loop()
}

func (r *Recorder) loop() {
	defer r.wg.Done()
	for {
		select {
		case rec := <-r.ch:
			if rec == nil {
				return
			}
			model.InsertUsage(r.db, rec)
		case <-r.done:
			for {
				select {
				case rec := <-r.ch:
					if rec == nil {
						return
					}
					model.InsertUsage(r.db, rec)
				default:
					return
				}
			}
		}
	}
}

func (r *Recorder) Stop() {
	select {
	case <-r.done:
		return
	default:
		close(r.done)
	}
	r.wg.Wait()
}

func (r *Recorder) Record(rec *model.UsageRecord) {
	select {
	case r.ch <- rec:
	default:
	}
}
