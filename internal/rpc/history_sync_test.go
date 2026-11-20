package rpc

import (
	"encoding/json"
	"testing"
)

func TestHistorySyncJobOptionalFields(t *testing.T) {
	job := HistorySyncJob{ChatID: 42, State: HistorySyncQueued, StartedAt: "2026-11-14T00:00:00Z", UpdatedAt: "2026-11-14T00:00:00Z"}
	raw, err := json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"oldest_id", "newest_id"} {
		if value, exists := fields[name]; !exists || value != nil {
			t.Fatalf("%s must be null when no message was saved: %s", name, raw)
		}
	}
	for _, name := range []string{"wait_seconds", "error_code", "error", "source"} {
		if _, exists := fields[name]; exists {
			t.Fatalf("%s must be absent outside its state: %s", name, raw)
		}
	}
	job.Source = HistorySyncBackground
	raw, err = json.Marshal(job)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fields); err != nil || fields["source"] != "background" {
		t.Fatalf("source of a background job: %s", raw)
	}
}
