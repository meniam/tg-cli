package vectors

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"
)

const (
	testModel  = "bge-m3"
	testDigest = "digest-a"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "vectors.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func randVec(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(rng.NormFloat64())
	}
	return v
}

func frag(chat, first int64, vec []float32) Fragment {
	return Fragment{
		FragmentMeta: FragmentMeta{
			ChatID: chat, FirstID: first, LastID: first + 3,
			StartsAt: 1000 + first, EndsAt: 2000 + first, Chars: 400,
			TextHash: fmt.Sprintf("hash-%d-%d", chat, first),
			Model:    testModel, ModelDigest: testDigest,
		},
		Vec: vec,
	}
}

func searchOpts() SearchOptions { return SearchOptions{Model: testModel, ModelDigest: testDigest} }

func keys(hits []Hit) []Key {
	out := make([]Key, len(hits))
	for i, h := range hits {
		out[i] = h.Key
	}
	return out
}

func keyLess(a, b Key) bool {
	return a.ChatID < b.ChatID || (a.ChatID == b.ChatID && a.FirstID < b.FirstID)
}

func TestOpenCreatesFileWithOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("new file mode = %o, want 600", got)
	}

	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	info, _ = os.Stat(path)
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("reopened file mode = %o, want 600", got)
	}
}

func TestOpenSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vectors.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	version, err := s.Version()
	if err != nil || version != SchemaVersion {
		t.Fatalf("Version = %d, %v; want %d", version, err, SchemaVersion)
	}
	if err := s.Put(context.Background(), []Fragment{frag(1, 1, []float32{1, 0})}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if n, _ := s.Count(context.Background(), testModel, testDigest); n != 1 {
		t.Errorf("rows after reopen = %d, want 1", n)
	}
	s.Close()

	raw, err := sql.Open("sqlite", dsn(path, "rw"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("Open accepted a database newer than the program")
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("Open(\"\") succeeded")
	}
}

// The file must never hold message text: the column set is the contract.
func TestSchemaHasNoTextColumn(t *testing.T) {
	s := openTemp(t)
	rows, err := s.read.Query(`SELECT name FROM pragma_table_info('fragments') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	want := []string{"chat_id", "topic_id", "first_id", "last_id", "starts_at", "ends_at", "chars",
		"text_hash", "model", "model_digest", "dim", "vec", "embedded_at"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("columns = %v, want %v", got, want)
	}
}

func TestSearchMatchesNaiveRanking(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	s := openTemp(t)
	const dim = 16
	var frags []Fragment
	for chat := int64(1); chat <= 5; chat++ {
		for i := int64(0); i < 60; i++ {
			frags = append(frags, frag(chat, 10+i*7, randVec(rng, dim)))
		}
	}
	// Exact duplicates give equal scores for every query.
	for i := 0; i < 40; i++ {
		src := frags[rng.Intn(len(frags))]
		dup := frag(int64(1+rng.Intn(5)), 5000+int64(i), append([]float32(nil), src.Vec...))
		frags = append(frags, dup)
	}
	if err := s.Put(context.Background(), frags); err != nil {
		t.Fatal(err)
	}

	type scored struct {
		key   Key
		score float32
	}
	ref := make([]scored, len(frags))
	for i, f := range frags {
		ref[i].key = Key{f.ChatID, f.FirstID}
	}

	for trial := 0; trial < 20; trial++ {
		query := randVec(rng, dim)
		if trial%4 == 0 { // query equal to a stored vector makes the tie group the top
			query = append([]float32(nil), frags[rng.Intn(len(frags))].Vec...)
		}
		q, _ := normalized(query)
		for i, f := range frags {
			u, _ := normalized(f.Vec)
			ref[i].score = dot(q, u)
		}
		sorted := append([]scored(nil), ref...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].score != sorted[j].score {
				return sorted[i].score > sorted[j].score
			}
			return keyLess(sorted[i].key, sorted[j].key)
		})
		for _, k := range []int{1, 5, 10, 50, len(frags), len(frags) + 20} {
			hits, err := s.Search(context.Background(), query, k, searchOpts())
			if err != nil {
				t.Fatal(err)
			}
			want := sorted[:min(k, len(sorted))]
			if len(hits) != len(want) {
				t.Fatalf("trial %d k=%d: %d hits, want %d", trial, k, len(hits), len(want))
			}
			for i := range want {
				if hits[i].Key != want[i].key || hits[i].Score != want[i].score {
					t.Fatalf("trial %d k=%d hit %d = %v %v, want %v %v",
						trial, k, i, hits[i].Key, hits[i].Score, want[i].key, want[i].score)
				}
			}
		}
	}
}

func TestDotMatchesFloat64(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for _, dim := range []int{1, 3, 4, 5, 16, 1023, 1024} {
		a, b := randVec(rng, dim), randVec(rng, dim)
		var want float64
		for i := range a {
			want += float64(a[i]) * float64(b[i])
		}
		if got := float64(dot(a, b)); math.Abs(got-want) > 1e-3*math.Max(1, math.Abs(want)) {
			t.Errorf("dim %d: dot = %v, want %v", dim, got, want)
		}
	}
}

func TestSearchEqualScoresOrderByKey(t *testing.T) {
	s := openTemp(t)
	same := []float32{0, 1, 0}
	// Inserted in reverse key order on purpose.
	var frags []Fragment
	for _, k := range []Key{{3, 1}, {2, 9}, {2, 4}, {1, 7}} {
		frags = append(frags, frag(k.ChatID, k.FirstID, append([]float32(nil), same...)))
	}
	frags = append(frags, frag(1, 1, []float32{1, 0, 0}))
	if err := s.Put(context.Background(), frags); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(context.Background(), same, 3, searchOpts())
	if err != nil {
		t.Fatal(err)
	}
	want := []Key{{1, 7}, {2, 4}, {2, 9}}
	if fmt.Sprint(keys(hits)) != fmt.Sprint(want) {
		t.Errorf("keys = %v, want %v", keys(hits), want)
	}
}

func TestSearchFilters(t *testing.T) {
	s := openTemp(t)
	rng := rand.New(rand.NewSource(3))
	var frags []Fragment
	for chat := int64(1); chat <= 4; chat++ {
		for i := int64(0); i < 10; i++ {
			frags = append(frags, frag(chat, i*10+1, randVec(rng, 8)))
		}
	}
	if err := s.Put(context.Background(), frags); err != nil {
		t.Fatal(err)
	}
	query := randVec(rng, 8)

	opts := searchOpts()
	opts.Allow = func(chat int64) bool { return chat == 2 || chat == 4 }
	hits, err := s.Search(context.Background(), query, 100, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 20 {
		t.Fatalf("Allow: %d hits, want 20", len(hits))
	}
	for _, h := range hits {
		if h.Key.ChatID != 2 && h.Key.ChatID != 4 {
			t.Errorf("Allow leaked chat %d", h.Key.ChatID)
		}
	}

	chat := int64(3)
	opts = searchOpts()
	opts.ChatID = &chat
	hits, err = s.Search(context.Background(), query, 100, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 10 {
		t.Fatalf("ChatID: %d hits, want 10", len(hits))
	}
	for _, h := range hits {
		if h.Key.ChatID != 3 {
			t.Errorf("ChatID leaked chat %d", h.Key.ChatID)
		}
	}

	// Both filters apply together.
	opts.Allow = func(c int64) bool { return c != 3 }
	hits, err = s.Search(context.Background(), query, 100, opts)
	if err != nil || len(hits) != 0 {
		t.Errorf("ChatID with Allow rejecting it = %d hits, %v; want none", len(hits), err)
	}
}

func TestSearchDoesNotMixModels(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	query := []float32{1, 0, 0, 0}
	other := frag(1, 1, []float32{1, 0, 0, 0}) // perfect match, but another digest
	other.ModelDigest = "digest-b"
	otherModel := frag(1, 2, []float32{1, 0, 0, 0, 0, 0}) // another model with another dimension
	otherModel.Model = "other"
	mine := frag(1, 3, []float32{0, 1, 0, 0})
	if err := s.Put(ctx, []Fragment{other, otherModel, mine}); err != nil {
		t.Fatal(err)
	}

	hits, err := s.Search(ctx, query, 10, searchOpts())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(keys(hits)) != fmt.Sprint([]Key{{1, 3}}) {
		t.Errorf("keys = %v, want only {1 3}", keys(hits))
	}

	opts := searchOpts()
	opts.ModelDigest = "digest-b"
	hits, err = s.Search(ctx, query, 10, opts)
	if err != nil || len(hits) != 1 || hits[0].Key != (Key{1, 1}) {
		t.Errorf("digest-b: %v, %v", hits, err)
	}

	opts = SearchOptions{Model: "unknown"}
	if hits, err = s.Search(ctx, query, 10, opts); err != nil || len(hits) != 0 {
		t.Errorf("unknown model: %v, %v; want empty", hits, err)
	}
	if _, err := s.Search(ctx, query, 10, SearchOptions{}); err == nil {
		t.Error("Search without a model succeeded")
	}
}

func TestSearchRejectsWrongQueryDimension(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.Put(ctx, []Fragment{frag(1, 1, []float32{1, 0, 0})}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Search(ctx, []float32{1, 0}, 5, searchOpts()); !errors.Is(err, ErrDimension) {
		t.Errorf("err = %v, want ErrDimension", err)
	}
	if _, err := s.Search(ctx, []float32{0, 0, 0}, 5, searchOpts()); !errors.Is(err, ErrBadVector) {
		t.Errorf("zero query err = %v, want ErrBadVector", err)
	}
	if hits, err := s.Search(ctx, []float32{1, 0, 0}, 0, searchOpts()); err != nil || hits != nil {
		t.Errorf("k=0: %v, %v; want nil", hits, err)
	}
}

func TestSearchStopsOnCanceledContext(t *testing.T) {
	s := openTemp(t)
	if err := s.Put(context.Background(), []Fragment{frag(1, 1, []float32{1, 0})}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(ctx, []float32{1, 0}, 5, searchOpts()); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestPutNormalizesOnACopy(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	vec := []float32{3, 4}
	if err := s.Put(ctx, []Fragment{frag(1, 1, vec)}); err != nil {
		t.Fatal(err)
	}
	if vec[0] != 3 || vec[1] != 4 {
		t.Errorf("Put modified the caller's vector: %v", vec)
	}
	hits, err := s.Search(ctx, []float32{30, 40}, 1, searchOpts())
	if err != nil || len(hits) != 1 {
		t.Fatalf("%v, %v", hits, err)
	}
	if math.Abs(float64(hits[0].Score)-1) > 1e-6 {
		t.Errorf("score = %v, want 1: stored vector is not unit length", hits[0].Score)
	}
}

func TestPutUpsertAndDelete(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	s.now = func() time.Time { return time.Unix(5000, 0) }

	if err := s.Put(ctx, []Fragment{frag(1, 1, []float32{1, 0}), frag(1, 2, []float32{0, 1}), frag(2, 1, []float32{1, 1})}); err != nil {
		t.Fatal(err)
	}
	hits, _ := s.Search(ctx, []float32{1, 0}, 1, searchOpts())
	if hits[0].Key != (Key{1, 1}) {
		t.Fatalf("top = %v, want {1 1}", hits[0].Key)
	}

	// Replace (1,1): the cached vectors must not serve the old one.
	changed := frag(1, 1, []float32{-1, 0})
	changed.TextHash = "new-hash"
	changed.LastID = 99
	if err := s.Put(ctx, []Fragment{changed}); err != nil {
		t.Fatal(err)
	}
	hits, _ = s.Search(ctx, []float32{1, 0}, 3, searchOpts())
	if len(hits) != 3 || hits[0].Key != (Key{2, 1}) || hits[2].Key != (Key{1, 1}) {
		t.Errorf("after upsert keys = %v", keys(hits))
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Key() != (Key{1, 1}) || list[0].TextHash != "new-hash" || list[0].LastID != 99 {
		t.Errorf("List = %+v", list)
	}
	if list[0].EmbeddedAt != 5000 || list[0].Dim != 2 || list[0].Model != testModel || list[0].ModelDigest != testDigest {
		t.Errorf("List metadata = %+v", list[0])
	}

	if err := s.Delete(ctx, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, 9, 9); err != nil {
		t.Errorf("deleting a missing fragment: %v", err)
	}
	hits, _ = s.Search(ctx, []float32{1, 0}, 10, searchOpts())
	if len(hits) != 2 {
		t.Errorf("after Delete %d hits, want 2", len(hits))
	}

	if err := s.DeleteChats(ctx, []int64{1, 77}); err != nil {
		t.Fatal(err)
	}
	hits, _ = s.Search(ctx, []float32{1, 0}, 10, searchOpts())
	if len(hits) != 1 || hits[0].Key != (Key{2, 1}) {
		t.Errorf("after DeleteChats keys = %v", keys(hits))
	}
	if n, _ := s.Count(ctx, testModel, testDigest); n != 1 {
		t.Errorf("Count = %d, want 1", n)
	}
	if n, _ := s.Count(ctx, testModel, "other-digest"); n != 0 {
		t.Errorf("Count for another digest = %d, want 0", n)
	}
}

func TestPutRejectsBadInputAtomically(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	if err := s.Put(ctx, []Fragment{frag(1, 1, []float32{1, 0, 0, 0})}); err != nil {
		t.Fatal(err)
	}

	nan := frag(1, 2, []float32{float32(math.NaN()), 1, 0, 0})
	noModel := frag(1, 3, []float32{1, 0, 0, 0})
	noModel.Model = ""
	mixedBatch := []Fragment{frag(1, 4, []float32{1, 0, 0, 0}), frag(1, 5, []float32{1, 0})}
	otherModel := frag(1, 6, []float32{1, 0})
	otherModel.Model = "second"
	storedMismatch := []Fragment{otherModel, frag(1, 7, []float32{1, 0})} // second row clashes with stored dim 4

	cases := []struct {
		name  string
		batch []Fragment
		want  error
	}{
		{"zero vector", []Fragment{frag(1, 2, []float32{0, 0, 0, 0})}, ErrBadVector},
		{"empty vector", []Fragment{frag(1, 2, nil)}, ErrBadVector},
		{"NaN", []Fragment{nan}, ErrBadVector},
		{"no model", []Fragment{noModel}, nil},
		{"mixed dims in a batch", mixedBatch, ErrDimension},
		{"dim differs from stored", storedMismatch, ErrDimension},
	}
	for _, tc := range cases {
		err := s.Put(ctx, tc.batch)
		if err == nil {
			t.Errorf("%s: Put succeeded", tc.name)
			continue
		}
		if tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	list, _ := s.List(ctx)
	if len(list) != 1 {
		t.Errorf("a rejected batch left %d rows, want only the original 1", len(list))
	}
}

func TestStats(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	empty, err := s.Stats(ctx)
	if err != nil || empty.Rows != 0 || empty.Bytes <= 0 {
		t.Fatalf("empty Stats = %+v, %v", empty, err)
	}
	rng := rand.New(rand.NewSource(4))
	var frags []Fragment
	for i := int64(0); i < 50; i++ {
		frags = append(frags, frag(1, i+1, randVec(rng, 1024)))
	}
	if err := s.Put(ctx, frags); err != nil {
		t.Fatal(err)
	}
	st, err := s.Stats(ctx)
	if err != nil || st.Rows != 50 || st.Bytes < empty.Bytes+50*4*1024 {
		t.Errorf("Stats = %+v, %v; want 50 rows and at least the vector bytes", st, err)
	}
}

// Readers and writers run together; -race checks the cache, and every search
// must see a consistent store (a vector dimension never changes under it).
func TestConcurrentSearchAndWrite(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	const dim = 8
	rng := rand.New(rand.NewSource(5))
	var seed []Fragment
	for i := int64(1); i <= 100; i++ {
		seed = append(seed, frag(1, i, randVec(rng, dim)))
	}
	if err := s.Put(ctx, seed); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + w)))
			for i := 0; i < 30; i++ {
				batch := []Fragment{frag(int64(2+w), int64(i+1), randVec(rng, dim)), frag(int64(2+w), int64(1000+i), randVec(rng, dim))}
				if err := s.Put(ctx, batch); err != nil {
					errs <- fmt.Errorf("put: %w", err)
					return
				}
				if i%10 == 9 {
					if err := s.DeleteChats(ctx, []int64{int64(2 + w)}); err != nil {
						errs <- fmt.Errorf("delete: %w", err)
						return
					}
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(200 + r)))
			for i := 0; i < 60; i++ {
				hits, err := s.Search(ctx, randVec(rng, dim), 5, searchOpts())
				if err != nil {
					errs <- fmt.Errorf("search: %w", err)
					return
				}
				for j := 1; j < len(hits); j++ {
					if hits[j-1].Score < hits[j].Score {
						errs <- fmt.Errorf("hits not sorted: %v then %v", hits[j-1].Score, hits[j].Score)
						return
					}
				}
			}
		}(r)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s := openTemp(t)
	first := s.Close()
	if second := s.Close(); fmt.Sprint(first) != fmt.Sprint(second) {
		t.Errorf("Close results differ: %v vs %v", first, second)
	}
}
