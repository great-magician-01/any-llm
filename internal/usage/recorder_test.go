package usage

import (
	"path/filepath"
	"testing"

	"github.com/great-magician-01/any-llm/internal/db"
	"github.com/great-magician-01/any-llm/internal/model"
)

func TestRecorderRecords(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	r := NewRecorder(d, 100)
	r.Start()
	defer r.Stop()

	r.Record(&model.UsageRecord{UpstreamName: "u", Model: "m", InFormat: "openai", UpFormat: "openai", TotalTokens: 10, Status: "ok"})
	r.Record(&model.UsageRecord{UpstreamName: "u", Model: "m", InFormat: "openai", UpFormat: "openai", TotalTokens: 20, Status: "ok"})

	r.Stop()

	records, total, err := model.UsageRecordsList(d, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("total=%d want 2", total)
	}
	sumTokens := 0
	for _, rec := range records {
		sumTokens += rec.TotalTokens
	}
	if sumTokens != 30 {
		t.Fatalf("sum tokens=%d want 30", sumTokens)
	}
}
