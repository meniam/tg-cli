package daemon

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"tg-cli/internal/rpc"
)

// semanticBlock returns the raw semantic_search block of the daemon status.
func semanticBlock(t *testing.T, d *daemon) string {
	t.Helper()
	result, err := d.status(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return string(fields["semantic_search"])
}

// The block is always in the daemon status and in the dashboard. With the
// feature off it says so and touches neither Ollama nor the vector file.
func TestSemanticSearchStatusIsAlwaysPresentWhenOff(t *testing.T) {
	d, tg, _, _ := dashboardFixture(t)
	d.login = newLoginManager(context.Background(), tg)
	emb := newFakeEmbedder()
	d.embedder = emb

	const want = `{"enabled":false,"state":"disabled","indexed":0,"total":0,"embedded":0,"removed":0}`
	if got := semanticBlock(t, d); got != want {
		t.Errorf("daemon status semantic_search = %s, want %s", got, want)
	}
	dash := callDashboard(t, d)
	if dash.SemanticSearch != (rpc.SemanticIndexStatus{State: rpc.SemanticIndexDisabled}) {
		t.Errorf("dashboard semantic_search = %+v, want disabled", dash.SemanticSearch)
	}
	if info, calls, _ := emb.counts(); info != 0 || calls != 0 {
		t.Errorf("the embedder was used: info %d, embed %d", info, calls)
	}
}

// An enabled indexer reports the same value in the daemon status and in the
// dashboard, including the reason of a failure.
func TestSemanticSearchStatusReportsTheIndexer(t *testing.T) {
	d, tg, _, _ := dashboardFixture(t)
	d.login = newLoginManager(context.Background(), tg)
	d.opts.SemanticSearch = SemanticSearchConfig{Enabled: true, Model: "bge-m3", Interval: 10 * time.Minute}
	d.semanticIndex = newSemanticIndexer(d, d.opts.SemanticSearch, nil)
	finished := int64(1_700_000_000)
	d.semanticIndex.update(func(s *rpc.SemanticIndexStatus) {
		s.State, s.Reason = rpc.SemanticIndexUnavailable, "Ollama is not reachable at http://127.0.0.1:11434"
		s.Model, s.Indexed, s.Total, s.FinishedAt = "bge-m3", 6382, 6500, &finished
	})

	const want = `{"enabled":true,"state":"unavailable","reason":"Ollama is not reachable at http://127.0.0.1:11434","model":"bge-m3","indexed":6382,"total":6500,"embedded":0,"removed":0,"finished_at":1700000000}`
	if got := semanticBlock(t, d); got != want {
		t.Errorf("daemon status semantic_search = %s, want %s", got, want)
	}
	dash := callDashboard(t, d)
	if !reflect.DeepEqual(dash.SemanticSearch, d.semanticIndexStatus()) || dash.SemanticSearch.State != rpc.SemanticIndexUnavailable {
		t.Errorf("dashboard semantic_search = %+v", dash.SemanticSearch)
	}
}
