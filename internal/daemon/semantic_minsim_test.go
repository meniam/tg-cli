package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/vectors"
)

// withGradedFragments adds two vector-only fragments to the shared fixture so
// the query vector [1 0] sees these cosines:
//
//	1.0  F2 (10-12)         0.9  G9 (60-61), added here
//	0.6  F1 (1-3)           0.4  G4 (50-51), added here
//	0.0  F4 (30-31, topic 9, its message 30 also holds the word "доставка")
//
// The closed chat (cosine 1.0) and the fragment of an old digest never reach
// the vector leg.
func withGradedFragments(t *testing.T, e *semanticEnv) {
	t.Helper()
	ctx := context.Background()
	msg := func(id int, text string) store.Message {
		return store.Message{ChatID: semPublicChat, ID: id, Date: semDate(id), Text: text, Raw: json.RawMessage(`{}`)}
	}
	if err := e.db.UpsertMessages(ctx, []store.Message{
		msg(50, "Рецепт борща"), msg(51, "Нужна свёкла"),
		msg(60, "Расписание поездов"), msg(61, "Поезд в семь утра"),
	}); err != nil {
		t.Fatal(err)
	}
	frag := func(first, last int, cosine float64) vectors.Fragment {
		return vectors.Fragment{FragmentMeta: vectors.FragmentMeta{
			ChatID: semPublicChat, FirstID: int64(first), LastID: int64(last),
			StartsAt: semDate(first), EndsAt: semDate(last), Chars: 300, TextHash: "h", Model: semModel, ModelDigest: semDigest,
		}, Vec: []float32{float32(cosine), float32(math.Sqrt(1 - cosine*cosine))}}
	}
	if err := e.vectors.Put(ctx, []vectors.Fragment{frag(60, 61, 0.9), frag(50, 51, 0.4)}); err != nil {
		t.Fatal(err)
	}
}

func hasSource(f *rpc.SemanticFragment, source string) bool {
	if f == nil {
		return false
	}
	for _, s := range f.Sources {
		if s == source {
			return true
		}
	}
	return false
}

func TestSemanticSearchDropsVectorHitsBelowTheThreshold(t *testing.T) {
	for _, tc := range []struct {
		name      string
		threshold float64
		dropped   int
		// vector says which fragments (by first id) the vector leg still finds.
		vector []int64
	}{
		{"0.5 drops 0.4 and 0.0", 0.5, 2, []int64{10, 60, 1}},
		{"a hit exactly at the threshold stays", 0.6, 2, []int64{10, 60, 1}},
		{"0.7 drops 0.6 too", 0.7, 3, []int64{10, 60}},
		{"0 turns the threshold off", 0, 0, []int64{10, 60, 1, 50, 30}},
		{"1 keeps only a perfect hit", 1, 4, []int64{10}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSemanticEnv(t)
			withGradedFragments(t, e)
			e.d.opts.SemanticSearch.MinSimilarity = tc.threshold
			result := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Limit: 100})
			if result.VectorDropped != tc.dropped {
				t.Errorf("vector_dropped = %d, want %d", result.VectorDropped, tc.dropped)
			}
			if result.VectorSkipped != "" {
				t.Errorf("vector_skipped = %q: the vector leg ran", result.VectorSkipped)
			}
			want := map[int64]bool{}
			for _, id := range tc.vector {
				want[id] = true
			}
			for _, id := range []int64{1, 10, 30, 50, 60} {
				f := find(result, semPublicChat, id)
				if got := hasSource(f, rpc.SourceVector); got != want[id] {
					t.Errorf("fragment %d: vector source = %v, want %v (%s)", id, got, want[id], dump(result))
				}
			}
			// A fragment that only the vector leg found disappears with its hit.
			for _, id := range []int64{10, 50, 60} {
				if f := find(result, semPublicChat, id); (f != nil) != want[id] {
					t.Errorf("vector-only fragment %d present = %v, want %v", id, f != nil, want[id])
				}
			}
			// The word legs do not depend on the threshold: message 30 (forum
			// topic, cosine 0.0) and message 20 stay found by their words.
			if f := find(result, semPublicChat, 30); f == nil || !hasSource(f, rpc.SourceExact) {
				t.Errorf("the word hit of fragment 30 was lost: %s", dump(result))
			}
			if find(result, semPublicChat, 20) == nil {
				t.Errorf("message 20 was lost: %s", dump(result))
			}
			for _, f := range result.Fragments {
				if f.ChatID == semClosedChat {
					t.Errorf("fragment of the closed chat: %+v", f)
				}
			}
		})
	}
}

func TestSemanticSearchRequestOverridesTheSettingBothWays(t *testing.T) {
	ptr := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name    string
		setting float64
		request *float64
		dropped int
	}{
		{"setting alone", 0.5, nil, 2},
		{"request turns a set threshold off", 0.5, ptr(0), 0},
		{"request raises the threshold", 0.5, ptr(0.7), 3},
		{"request sets a threshold where the setting has none", 0, ptr(0.5), 2},
		{"no setting and no request", 0, nil, 0},
		{"request lowers the threshold", 0.7, ptr(0.3), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newSemanticEnv(t)
			withGradedFragments(t, e)
			e.d.opts.SemanticSearch.MinSimilarity = tc.setting
			result := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Limit: 100, MinSimilarity: tc.request})
			if result.VectorDropped != tc.dropped {
				t.Errorf("vector_dropped = %d, want %d", result.VectorDropped, tc.dropped)
			}
		})
	}
}

func TestSemanticSearchRefusesAThresholdOutsideZeroToOne(t *testing.T) {
	e := newSemanticEnv(t)
	for _, value := range []float64{1.01, -0.01, 2, -1} {
		v := value
		_, err := e.d.searchSemantic(context.Background(), params(t, rpc.SemanticSearchParams{Query: "доставка", MinSimilarity: &v}))
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams || !strings.Contains(rpcErr.Message, "min_similarity") {
			t.Errorf("min_similarity %v: err = %v, want invalid params naming the field", value, err)
		}
	}
	// The bounds themselves are valid.
	for _, value := range []float64{0, 1} {
		v := value
		if _, err := e.d.searchSemantic(context.Background(), params(t, rpc.SemanticSearchParams{Query: "доставка", MinSimilarity: &v})); err != nil {
			t.Errorf("min_similarity %v: %v", value, err)
		}
	}
}

// When every vector hit is below the threshold the vector leg still ran: no
// reason is given and the fragments come from the word legs alone.
func TestSemanticSearchAllVectorHitsBelowTheThreshold(t *testing.T) {
	e := newSemanticEnv(t)
	withGradedFragments(t, e)
	// The query vector [0 -1] has a cosine of 0 or less with every fragment.
	e.embedder.vector = []float32{0, -1}
	e.d.opts.SemanticSearch.MinSimilarity = 0.5
	result := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Limit: 100})
	if result.VectorSkipped != "" {
		t.Errorf("vector_skipped = %q, want empty", result.VectorSkipped)
	}
	// F1, F2, F4, G4 and G9 are all in the public chat; the closed chat and the old digest are not counted.
	if result.VectorDropped != 5 {
		t.Errorf("vector_dropped = %d, want 5", result.VectorDropped)
	}
	if len(result.Fragments) == 0 || find(result, semPublicChat, 20) == nil {
		t.Fatalf("no word fragments: %s", dump(result))
	}
	for _, f := range result.Fragments {
		if hasSource(&f, rpc.SourceVector) {
			t.Errorf("fragment %d has a vector source: %+v", f.FirstID, f)
		}
		if f.ChatID == semClosedChat || f.FirstID == 10 || f.FirstID == 50 || f.FirstID == 60 {
			t.Errorf("a vector-only or closed fragment survived: %+v", f)
		}
	}
}

// The threshold changes nothing where there is no vector leg or no index, and
// the answer then reports no dropped hits.
func TestSemanticSearchThresholdWithoutAVectorLeg(t *testing.T) {
	for name, arrange := range map[string]func(e *semanticEnv){
		"feature off": func(e *semanticEnv) { e.d.vectors, e.d.embedder = nil, nil },
		"index empty": func(e *semanticEnv) {
			if err := e.vectors.DeleteChats(context.Background(), []int64{semPublicChat, semClosedChat}); err != nil {
				t.Fatal(err)
			}
		},
		"ollama error": func(e *semanticEnv) { e.embedder.embedErr = errors.New("boom") },
	} {
		t.Run(name, func(t *testing.T) {
			e := newSemanticEnv(t)
			arrange(e)
			e.d.opts.SemanticSearch.MinSimilarity = 0.9
			result := e.search(t, rpc.SemanticSearchParams{Query: "доставка"})
			if result.VectorSkipped == "" || result.VectorDropped != 0 {
				t.Errorf("vector_skipped = %q, vector_dropped = %d", result.VectorSkipped, result.VectorDropped)
			}
		})
	}
}

// vector_dropped is always in the answer, 0 included.
func TestSemanticSearchResultAlwaysCarriesVectorDropped(t *testing.T) {
	raw, err := json.Marshal(rpc.SemanticSearchResult{Fragments: []rpc.SemanticFragment{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"vector_dropped":0`) {
		t.Errorf("result = %s", raw)
	}
	raw, _ = json.Marshal(rpc.SemanticSearchParams{Query: "q"})
	if strings.Contains(string(raw), "min_similarity") {
		t.Errorf("an unset override is sent: %s", raw)
	}
}
