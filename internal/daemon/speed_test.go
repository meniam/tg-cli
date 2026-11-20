//go:build speed

package daemon

// The speed harness of the semantic search (plan step 12). It is not part of the
// ordinary test run: the build tag keeps it out of `go test ./...`, and without
// SPEED_STORE it skips itself. It needs a copy of store.db and a running Ollama
// with the embedding model, and it writes only numbers and step names into the
// report: no message text and no query text ever reaches the report.
//
//	SPEED_STORE=/path/to/copy-of-store.db \
//	SPEED_OUT=/path/to/speed.md \
//	go test -tags speed -run TestSpeed -timeout 60m ./internal/daemon
//
// Environment: SPEED_STORE (required, a copy: the harness copies it again for
// every run and never opens the file itself), SPEED_OLLAMA (default
// http://127.0.0.1:11434), SPEED_MODEL (default bge-m3), SPEED_OUT (default
// speed.md in the temporary directory of the OS), SPEED_SYNTH (comma-separated
// vector counts of the synthetic volumes, default 6000,64000,640000; the largest
// file takes about 2.6 GB of disk and memory), SPEED_TMP (the directory of the
// working files, default a temporary one).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"tg-cli/internal/embed"
	"tg-cli/internal/rpc"
	"tg-cli/internal/stem"
	"tg-cli/internal/store"
	"tg-cli/internal/textnorm"
	"tg-cli/internal/vectors"
)

const (
	speedQueryCount = 50 // the size of the query set
	speedRepeats    = 5  // timed passes over the set, after one untimed warm-up pass
	speedOpenRuns   = 3  // timed opens of a fresh copy of the database, after one warm-up
	speedColdRuns   = 5  // cold samples of the vector load and of the end-to-end search
	speedLegLimit   = 60 // candidates of a leg: the default limit 20 times semanticLegFactor
	speedTopK       = 10 // fragments the synthetic volumes return
	speedDim        = 1024
)

// speedRun carries the settings, the report and the state the steps share.
type speedRun struct {
	t      *testing.T
	ctx    context.Context
	src    string
	ollama string
	model  string
	tmp    string
	out    strings.Builder
}

func (s *speedRun) line(format string, args ...any) { fmt.Fprintf(&s.out, format+"\n", args...) }

func (s *speedRun) section(title string) { s.line("\n## %s\n", title) }

// table writes a Markdown table.
func (s *speedRun) table(header []string, rows ...[]string) {
	s.line("| %s |", strings.Join(header, " | "))
	dashes := make([]string, len(header))
	for i := range dashes {
		dashes[i] = "---"
	}
	s.line("| %s |", strings.Join(dashes, " | "))
	for _, row := range rows {
		s.line("| %s |", strings.Join(row, " | "))
	}
	s.line("")
}

var latencyHeader = []string{"Step", "Samples", "p50 ms", "p95 ms", "p99 ms", "max ms"}

func ms(d time.Duration) string { return fmt.Sprintf("%.2f", float64(d)/float64(time.Millisecond)) }

// percentile is the nearest-rank percentile of sorted samples.
func percentile(sorted []time.Duration, p float64) time.Duration {
	rank := int(math.Ceil(p*float64(len(sorted)))) - 1
	return sorted[max(0, min(rank, len(sorted)-1))]
}

// latencyRow is one row of a latency table; no samples give "n/a" cells.
func latencyRow(name string, samples []time.Duration) []string {
	if len(samples) == 0 {
		return []string{name, "0", "n/a", "n/a", "n/a", "n/a"}
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return []string{name, strconv.Itoa(len(sorted)), ms(percentile(sorted, 0.50)), ms(percentile(sorted, 0.95)), ms(percentile(sorted, 0.99)), ms(sorted[len(sorted)-1])}
}

// timeQueries runs fn for every query once untimed (the warm-up), then repeats
// times timed, and returns the timed samples. A failure stops the run: a
// measurement of a failing call means nothing.
func (s *speedRun) timeQueries(name string, count, repeats int, fn func(i int) error) []time.Duration {
	s.t.Helper()
	for i := range count {
		if err := fn(i); err != nil {
			s.t.Fatalf("%s: warm-up call %d: %v", name, i, err)
		}
	}
	samples := make([]time.Duration, 0, count*repeats)
	for range repeats {
		for i := range count {
			start := time.Now()
			err := fn(i)
			samples = append(samples, time.Since(start))
			if err != nil {
				s.t.Fatalf("%s: call %d: %v", name, i, err)
			}
		}
	}
	return samples
}

func fileSize(path string) int64 {
	var total int64
	for _, suffix := range []string{"", "-wal"} {
		if info, err := os.Stat(path + suffix); err == nil {
			total += info.Size()
		}
	}
	return total
}

func mb(bytes int64) string { return fmt.Sprintf("%.1f", float64(bytes)/1e6) }

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func userVersion(path string) (int, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var v int
	err = db.QueryRow(`PRAGMA user_version`).Scan(&v)
	return v, err
}

func TestSpeed(t *testing.T) {
	src := os.Getenv("SPEED_STORE")
	if src == "" {
		t.Skip("SPEED_STORE is not set: the speed harness needs a copy of store.db")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("SPEED_STORE: %v", err)
	}
	s := &speedRun{
		t: t, ctx: context.Background(), src: src,
		ollama: envOr("SPEED_OLLAMA", "http://127.0.0.1:11434"),
		model:  envOr("SPEED_MODEL", "bge-m3"),
		tmp:    os.Getenv("SPEED_TMP"),
	}
	if s.tmp == "" {
		s.tmp = t.TempDir()
	} else if err := os.MkdirAll(s.tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	outPath := envOr("SPEED_OUT", filepath.Join(os.TempDir(), "speed.md"))

	s.line("# Semantic search speed")
	s.line("")
	s.line("Generated %s. Only numbers and step names: no message text, no query text.", time.Now().UTC().Format(time.RFC3339))
	s.line("")
	s.line("- Go %s on %s/%s, %d CPUs", runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU())
	s.line("- Model setting %q; every timed step is preceded by one untimed warm-up pass, and a query step runs %d queries %d times", s.model, speedQueryCount, speedRepeats)

	workPath, db := s.stepOpen()
	defer func() { _ = db.Close() }()
	queries, messages := s.stepQueries(db)

	client, info, ollamaErr := s.connect()
	d := &daemon{
		opts:  Options{Paths: Paths{Dir: s.tmp, DatabaseDir: filepath.Join(s.tmp, "build")}, SemanticSearch: SemanticSearchConfig{Enabled: true, OllamaURL: s.ollama, Model: s.model, Batch: embed.DefaultBatch, Interval: 10 * time.Minute}},
		store: db, log: discardLogger(),
	}
	if err := os.MkdirAll(d.opts.Paths.DatabaseDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var vecs [][]float32
	if ollamaErr == nil {
		d.embedder = client
		s.stepBuild(d, client, messages)
		defer func() {
			if d.vectors != nil {
				_ = d.vectors.Close()
			}
		}()
		texts := queries
		var err error
		if vecs, err = client.Embed(s.ctx, texts); err != nil {
			t.Fatalf("embed the query set: %v", err)
		}
	} else {
		s.section("Index build, vector leg, embedding, end to end")
		s.line("Skipped: Ollama is not available (%s). The steps that need it are not measured.\n", vectorSkipReason(ollamaErr))
	}

	s.stepLegs(db, workPath, d, info, queries, vecs)
	if ollamaErr == nil {
		s.stepEmbedQuery(client, queries)
		s.stepEndToEnd(d, queries)
	}
	s.stepSynthetic()
	s.stepSizes(workPath, d)

	if err := os.WriteFile(outPath, []byte(s.out.String()), 0o600); err != nil {
		t.Fatalf("write the report: %v", err)
	}
	t.Logf("speed report written to %s", outPath)
}

// stepOpen opens a fresh copy of the database with its migrations, repeatedly,
// and returns the last migrated copy still open for the other steps.
func (s *speedRun) stepOpen() (string, *store.DB) {
	t := s.t
	var samples []time.Duration
	var before, after int64
	var versionBefore, versionAfter int
	path := ""
	for run := 0; run <= speedOpenRuns; run++ { // run 0 is the warm-up
		path = filepath.Join(s.tmp, fmt.Sprintf("store-%d.db", run))
		_ = os.Remove(path)
		if err := copyFile(s.src, path); err != nil {
			t.Fatal(err)
		}
		before = fileSize(path)
		var err error
		if versionBefore, err = userVersion(path); err != nil {
			t.Fatalf("read the schema version of the copy: %v", err)
		}
		start := time.Now()
		db, err := store.Open(path)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("open the copy: %v", err)
		}
		if versionAfter, err = db.Version(); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		after = fileSize(path)
		if run > 0 {
			samples = append(samples, elapsed)
		}
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.section("1. Open a copy of the database with migrations")
	s.line("Every run copies the file again, so a migration that is already applied in the copy costs nothing on later runs of the same file.\n")
	s.table(latencyHeader, latencyRow("store.Open of a fresh copy", samples))
	s.table([]string{"Quantity", "Value"},
		[]string{"schema version before", strconv.Itoa(versionBefore)},
		[]string{"schema version after", strconv.Itoa(versionAfter)},
		[]string{"store.db size before, MB", mb(before)},
		[]string{"store.db size after, MB", mb(after)})
	return path, db
}

// stepQueries builds the query set from the words of the database itself: the
// most frequent words of four letters and more, 25 alone and 25 in pairs. It
// returns the queries and the messages the indexer will read, as a count only.
func (s *speedRun) stepQueries(db *store.DB) ([]string, int) {
	msgs, err := db.MessagesForFragments(s.ctx, store.ChatFilter{})
	if err != nil {
		s.t.Fatal(err)
	}
	freq := map[string]int{}
	for _, m := range msgs {
		for _, w := range strings.FieldsFunc(m.Text, func(r rune) bool { return !unicode.IsLetter(r) }) {
			if w = strings.ToLower(w); len([]rune(w)) >= 4 {
				freq[w]++
			}
		}
	}
	words := make([]string, 0, len(freq))
	for w := range freq {
		words = append(words, w)
	}
	sort.Slice(words, func(i, j int) bool {
		if freq[words[i]] != freq[words[j]] {
			return freq[words[i]] > freq[words[j]]
		}
		return words[i] < words[j]
	})
	if len(words) < speedQueryCount {
		s.t.Fatalf("the database has %d distinct words of four letters or more, the query set needs %d", len(words), speedQueryCount)
	}
	half := speedQueryCount / 2
	queries := make([]string, 0, speedQueryCount)
	queries = append(queries, words[:half]...)
	for i := range half {
		queries = append(queries, words[i]+" "+words[half+i])
	}
	s.section("Query set")
	s.line("%d queries from the most frequent words of the database: %d single words and %d pairs. Messages the indexer reads: %d.\n", len(queries), half, half, len(msgs))
	return queries, len(msgs)
}

// connect builds the Ollama client and asks it for the model.
func (s *speedRun) connect() (*embed.Client, embed.ModelInfo, error) {
	client, err := embed.New(embed.Options{BaseURL: s.ollama, Model: s.model, Batch: embed.DefaultBatch})
	if err != nil {
		s.t.Fatalf("Ollama client: %v", err)
	}
	info, err := client.Info(s.ctx)
	if err != nil {
		return client, embed.ModelInfo{}, err
	}
	digest := info.Digest
	if len(digest) > 12 {
		digest = digest[:12]
	}
	s.line("- Ollama model %s, digest %s, %d dimensions", info.Name, digest, info.Dim)
	return client, info, nil
}

// stepBuild builds vectors.db with a full pass of the indexer and the real
// client, then runs a second pass that finds nothing to do.
func (s *speedRun) stepBuild(d *daemon, client *embed.Client, messages int) {
	t := s.t
	path := vectorsPath(d.opts.Paths)
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	vec, err := vectors.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.vectors = vec
	// The model is loaded once before the clock starts, as a daemon that has
	// already served a query would have it.
	if _, err := client.Embed(s.ctx, []string{"warm up"}); err != nil {
		t.Fatalf("warm up the model: %v", err)
	}
	x := newSemanticIndexer(d, d.opts.SemanticSearch, nil)
	x.root = s.ctx
	d.semanticIndex = x

	run := func() (time.Duration, rpc.SemanticIndexStatus) {
		rctx := x.reserve()
		start := time.Now()
		out := x.pass(rctx)
		elapsed := time.Since(start)
		x.finish(out, s.ctx)
		return elapsed, x.status()
	}
	full, st := run()
	again, _ := run()
	// A closed and reopened file is checkpointed, so its size is the file alone.
	if err := vec.Close(); err != nil {
		t.Fatal(err)
	}
	size := fileSize(path)
	if d.vectors, err = vectors.Open(path); err != nil {
		t.Fatal(err)
	}
	x.d = d

	s.section("2. Build vectors.db with a full indexer pass")
	s.line("One full pass with the real Ollama client and the production pause between batches (%v). The model was loaded before the clock started. The second pass changes nothing and shows the cost of a run that finds no work.\n", semanticIndexPause)
	fragsPerSecond := "n/a"
	if full > 0 {
		fragsPerSecond = fmt.Sprintf("%.1f", float64(st.Embedded)/full.Seconds())
	}
	s.table([]string{"Quantity", "Value"},
		[]string{"state after the pass", st.State},
		[]string{"messages read", strconv.Itoa(messages)},
		[]string{"fragments long enough for the index", strconv.Itoa(st.Total)},
		[]string{"fragments embedded", strconv.Itoa(st.Embedded)},
		[]string{"fragments stored", strconv.Itoa(st.Indexed)},
		[]string{"full pass, s", fmt.Sprintf("%.1f", full.Seconds())},
		[]string{"fragments per second", fragsPerSecond},
		[]string{"second pass with nothing to do, ms", ms(again)},
		[]string{"vectors.db size, MB", mb(size)})
	if st.State != rpc.SemanticIndexIdle {
		s.line("The pass did not end idle, so the vector steps below may have no rows (reason class: %s).\n", st.State)
	}
}

// legQuery mirrors the SQL of store.messageLeg without the access filter.
func legQuery(table string) string {
	return `SELECT m.id,m.chat_id,m.text,m.date,m.deleted_at,m.topic_id,c.type,c.username FROM ` + table +
		` JOIN messages m ON m.rowid=` + table + `.rowid LEFT JOIN chats c ON c.id=m.chat_id WHERE ` + table +
		` MATCH ? AND m.deleted_at IS NULL ORDER BY bm25(` + table + `),m.rowid LIMIT ?`
}

func runLeg(ctx context.Context, db *sql.DB, query, match string) error {
	rows, err := db.QueryContext(ctx, query, match, speedLegLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, chat, date, deleted, topic sql.NullInt64
		var text, kind, username sql.NullString
		if err := rows.Scan(&id, &chat, &text, &date, &deleted, &topic, &kind, &username); err != nil {
			return err
		}
	}
	return rows.Err()
}

// stepLegs measures every leg of the search on the query set, the fusion, the
// whole ordinary search and the vector load of the real file.
func (s *speedRun) stepLegs(db *store.DB, dbPath string, d *daemon, info embed.ModelInfo, queries []string, vecs [][]float32) {
	t := s.t
	ctx := s.ctx
	n := len(queries)
	raw, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	exactQuery, stemQuery := legQuery("messages_fts"), legQuery("messages_stem_fts")
	var exactMatch, stemMatch []string
	for _, q := range queries {
		exactMatch = append(exactMatch, textnorm.Query(q, false))
		stemMatch = append(stemMatch, stem.Query(q, false))
	}

	rows := [][]string{
		latencyRow("exact leg, messages_fts (SQL mirror of the leg)", s.timeQueries("exact leg", n, speedRepeats, func(i int) error {
			return runLeg(ctx, raw, exactQuery, exactMatch[i])
		})),
		latencyRow("stemmed leg, messages_stem_fts (SQL mirror of the leg)", s.timeQueries("stemmed leg", n, speedRepeats, func(i int) error {
			if stemMatch[i] == "" {
				return nil
			}
			return runLeg(ctx, raw, stemQuery, stemMatch[i])
		})),
		latencyRow("store.WordLegs, both word legs", s.timeQueries("word legs", n, speedRepeats, func(i int) error {
			_, _, err := db.WordLegs(ctx, store.WordLegOptions{Query: queries[i], Limit: speedLegLimit})
			return err
		})),
		latencyRow("store.Search, scope messages (ordinary search, two legs)", s.timeQueries("ordinary search", n, speedRepeats, func(i int) error {
			_, err := db.Search(ctx, store.SearchOptions{Query: queries[i], Scopes: []string{"messages"}, Limit: 20})
			return err
		})),
	}

	s.section("3. Search legs and fusion on the query set")
	s.line("The two SQL-mirror rows run the query of `store.messageLeg` on a separate read-only connection without the access filter: they separate the legs, which the store does not export. `store.WordLegs` and `store.Search` are the real functions. The ordinary search is the comparison with the baseline of 12 ms p50 and p95 measured through `tg search` with the process start included.\n")

	if vecs != nil && d.vectors != nil {
		all, err := d.vectors.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var metas []vectors.FragmentMeta
		for _, m := range all {
			if m.Model == info.Name && m.ModelDigest == info.Digest {
				metas = append(metas, m)
			}
		}
		options := vectors.SearchOptions{Model: info.Name, ModelDigest: info.Digest}
		exact := make([][]store.WordMatch, n)
		stemmed := make([][]store.WordMatch, n)
		hits := make([][]vectors.Hit, n)
		for i := range n {
			if exact[i], stemmed[i], err = db.WordLegs(ctx, store.WordLegOptions{Query: queries[i], Limit: speedLegLimit}); err != nil {
				t.Fatal(err)
			}
		}
		rows = append(rows,
			latencyRow(fmt.Sprintf("vector leg, vectors.Store.Search top-%d (%d fragments, index in memory)", speedLegLimit, len(metas)), s.timeQueries("vector leg", n, speedRepeats, func(i int) error {
				h, err := d.vectors.Search(ctx, vecs[i], speedLegLimit, options)
				hits[i] = h
				return err
			})),
			latencyRow("fusion, fuseFragments", s.timeQueries("fusion", n, speedRepeats, func(i int) error {
				fuseFragments(20, exact[i], stemmed[i], hits[i], metas)
				return nil
			})))
		s.table(latencyHeader, rows...)

		// The load of the real index: a reopened store has nothing in memory.
		var loads []time.Duration
		for range speedColdRuns {
			if err := d.vectors.Close(); err != nil {
				t.Fatal(err)
			}
			if d.vectors, err = vectors.Open(vectorsPath(d.opts.Paths)); err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if _, err := d.vectors.Search(ctx, vecs[0], speedLegLimit, options); err != nil {
				t.Fatal(err)
			}
			loads = append(loads, time.Since(start))
		}
		s.table(latencyHeader, latencyRow(fmt.Sprintf("vector index load and first query (%d fragments, reopened file)", len(metas)), loads))
		return
	}
	s.table(latencyHeader, rows...)
	s.line("The vector leg and the fusion are not measured: no query embeddings (Ollama is not available).\n")
}

// stepEmbedQuery measures the embedding of one query: warm, and cold after the
// model is unloaded.
func (s *speedRun) stepEmbedQuery(client *embed.Client, queries []string) {
	t := s.t
	warm := s.timeQueries("embed one query, warm", len(queries), 2, func(i int) error {
		_, err := client.Embed(s.ctx, []string{queries[i]})
		return err
	})
	s.section("4. Embedding of one query through Ollama")
	rows := [][]string{latencyRow("warm (model loaded)", warm)}
	var cold []time.Duration
	unloaded := 0
	for i := range speedColdRuns {
		if !s.unload() {
			break
		}
		unloaded++
		start := time.Now()
		if _, err := client.Embed(s.ctx, []string{queries[i%len(queries)]}); err != nil {
			t.Fatalf("embed after unload: %v", err)
		}
		cold = append(cold, time.Since(start))
	}
	rows = append(rows, latencyRow("cold (first call after the model was unloaded)", cold))
	s.table(latencyHeader, rows...)
	if unloaded == 0 {
		s.line("Cold start unavailable: the harness could not unload the model (`/api/embed` with `keep_alive` 0 left it listed in `/api/ps`). The client keeps the model for 30 minutes, so waiting for it to leave on its own is not practical.\n")
	}
	// Leave the model loaded for the steps that follow.
	if _, err := client.Embed(s.ctx, []string{queries[0]}); err != nil {
		t.Fatal(err)
	}
}

// unload asks Ollama to drop the model and reports whether it left /api/ps
// within 15 seconds.
func (s *speedRun) unload() bool {
	body, _ := json.Marshal(map[string]any{"model": s.model, "input": []string{}, "keep_alive": 0})
	req, err := http.NewRequestWithContext(s.ctx, http.MethodPost, strings.TrimRight(s.ollama, "/")+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !s.loaded() {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

// loaded reports whether /api/ps lists the model.
func (s *speedRun) loaded() bool {
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, strings.TrimRight(s.ollama, "/")+"/api/ps", nil)
	if err != nil {
		return true
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	var ps struct {
		Models []struct{ Name, Model string } `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&ps) != nil {
		return true
	}
	for _, m := range ps.Models {
		if m.Name == s.model || m.Model == s.model || strings.HasPrefix(m.Name, s.model+":") || strings.HasPrefix(m.Model, s.model+":") {
			return true
		}
	}
	return false
}

// stepEndToEnd measures the handler of the daemon on the query set: warm, cold
// (the index is not in memory) and with Ollama out of reach.
func (s *speedRun) stepEndToEnd(d *daemon, queries []string) {
	t := s.t
	params := make([]json.RawMessage, len(queries))
	for i, q := range queries {
		params[i], _ = json.Marshal(rpc.SemanticSearchParams{Query: q})
	}
	call := func(i int) (rpc.SemanticSearchResult, error) {
		res, err := d.searchSemantic(s.ctx, params[i])
		if err != nil {
			return rpc.SemanticSearchResult{}, err
		}
		return res.(rpc.SemanticSearchResult), nil
	}
	skipped := 0
	warm := s.timeQueries("end to end, warm", len(queries), speedRepeats, func(i int) error {
		res, err := call(i)
		if res.VectorSkipped != "" {
			skipped++
		}
		return err
	})

	var cold []time.Duration
	for run := range speedColdRuns {
		if err := d.vectors.Close(); err != nil {
			t.Fatal(err)
		}
		vec, err := vectors.Open(vectorsPath(d.opts.Paths))
		if err != nil {
			t.Fatal(err)
		}
		d.vectors = vec
		start := time.Now()
		res, err := call(run % len(queries))
		cold = append(cold, time.Since(start))
		if err != nil {
			t.Fatal(err)
		}
		if res.VectorSkipped != "" {
			skipped++
		}
	}

	// Ollama out of reach: a closed port on the loopback. The handler goes on
	// with the word legs and reports vector_skipped.
	dead, err := embed.New(embed.Options{BaseURL: "http://127.0.0.1:1", Model: s.model})
	if err != nil {
		t.Fatal(err)
	}
	live := d.embedder
	d.embedder = dead
	var skippedDead int
	words := s.timeQueries("end to end, without Ollama", len(queries), speedRepeats, func(i int) error {
		res, err := call(i)
		if res.VectorSkipped != "" {
			skippedDead++
		}
		return err
	})
	d.embedder = live

	s.section("5. End to end: the searchSemantic handler")
	s.line("Each call embeds its query through Ollama, so the warm rows include the warm embedding of step 4. A cold sample reopens vectors.db first, so the index is not in memory.\n")
	s.table(latencyHeader,
		latencyRow("warm (index in memory, Ollama warm)", warm),
		latencyRow("cold (index loaded by the call)", cold),
		latencyRow("without Ollama (words only, vector_skipped)", words))
	s.line("Calls that reported `vector_skipped` while Ollama was reachable: %d (expect 0). Calls without Ollama that reported it: %d.\n", skipped, skippedDead)
}

// stepSynthetic measures the vector leg on synthetic volumes.
func (s *speedRun) stepSynthetic() {
	t := s.t
	sizes := []int{6000, 64000, 640000}
	if env := os.Getenv("SPEED_SYNTH"); env != "" {
		sizes = nil
		for _, part := range strings.Split(env, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil || n < 1 {
				t.Fatalf("SPEED_SYNTH: %q is not a positive number", part)
			}
			sizes = append(sizes, n)
		}
	}
	s.section("6. Vector leg on synthetic volumes")
	s.line("Random unit vectors of %d numbers, top-%d, %d random queries, %d timed passes after one warm-up. Load is the first query on a reopened file. The memory columns come from the Go runtime: heap in use after the load (before the load, after a collection) and the highest heap seen while loading (sampled every 20 ms, so a lower bound).\n", speedDim, speedTopK, speedQueryCount, speedRepeats)
	header := []string{"Vectors", "File MB", "Load ms", "Heap after load MB", "Peak heap MB", "Query p50 ms", "Query p95 ms", "Query p99 ms", "Query max ms"}
	var rows [][]string
	for _, n := range sizes {
		path := filepath.Join(s.tmp, fmt.Sprintf("synthetic-%d.db", n))
		rows = append(rows, s.syntheticVolume(path, n))
	}
	s.table(header, rows...)
}

func (s *speedRun) syntheticVolume(path string, n int) []string {
	t := s.t
	ctx := s.ctx
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	vec, err := vectors.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(int64(n)))
	randVector := func() []float32 {
		v := make([]float32, speedDim)
		for i := range v {
			v[i] = float32(rng.NormFloat64())
		}
		return v
	}
	const batch = 500
	for done := 0; done < n; {
		var frags []vectors.Fragment
		for i := 0; i < batch && done < n; i, done = i+1, done+1 {
			first := int64(done/500)*10 + 1
			frags = append(frags, vectors.Fragment{
				FragmentMeta: vectors.FragmentMeta{ChatID: int64(done % 500), FirstID: first, LastID: first + 5, StartsAt: 1, EndsAt: 2, Chars: 700, TextHash: "speed", Model: "speed", ModelDigest: "speed"},
				Vec:          randVector(),
			})
		}
		if err := vec.Put(ctx, frags); err != nil {
			t.Fatal(err)
		}
	}
	if err := vec.Close(); err != nil {
		t.Fatal(err)
	}
	size := fileSize(path)

	if vec, err = vectors.Open(path); err != nil {
		t.Fatal(err)
	}
	options := vectors.SearchOptions{Model: "speed", ModelDigest: "speed"}
	queries := make([][]float32, speedQueryCount)
	for i := range queries {
		queries[i] = randVector()
	}
	runtime.GC()
	var peak uint64
	var mu sync.Mutex
	stop := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			mu.Lock()
			peak = max(peak, m.HeapAlloc)
			mu.Unlock()
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}()
	start := time.Now()
	if _, err := vec.Search(ctx, queries[0], speedTopK, options); err != nil {
		t.Fatal(err)
	}
	load := time.Since(start)
	close(stop)
	<-sampled
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	mu.Lock()
	peakHeap := max(peak, after.HeapAlloc)
	mu.Unlock()

	samples := s.timeQueries(fmt.Sprintf("synthetic %d", n), len(queries), speedRepeats, func(i int) error {
		_, err := vec.Search(ctx, queries[i], speedTopK, options)
		return err
	})
	if err := vec.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	row := latencyRow("", samples)
	return []string{strconv.Itoa(n), mb(size), ms(load), mb(int64(after.HeapAlloc)), mb(int64(peakHeap)), row[2], row[3], row[4], row[5]}
}

// stepSizes writes the sizes of the files.
func (s *speedRun) stepSizes(workPath string, d *daemon) {
	s.section("7. Files on disk")
	rows := [][]string{{"store.db after migrations", mb(fileSize(workPath))}}
	if d.vectors != nil {
		rows = append(rows, []string{"vectors.db", mb(fileSize(vectorsPath(d.opts.Paths)))})
	}
	var stemBytes sql.NullInt64
	if raw, err := sql.Open("sqlite", "file:"+workPath+"?mode=ro"); err == nil {
		defer raw.Close()
		if err := raw.QueryRow(`SELECT SUM(pgsize) FROM dbstat WHERE name LIKE 'messages_stem_fts%'`).Scan(&stemBytes); err == nil && stemBytes.Valid {
			rows = append(rows, []string{"messages_stem_fts, tables and indexes together", mb(stemBytes.Int64)})
		}
	}
	s.table([]string{"File", "MB"}, rows...)
}
