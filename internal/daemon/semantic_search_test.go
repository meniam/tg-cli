package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tg-cli/internal/embed"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/vectors"
)

const (
	semModel  = "bge-m3:latest"
	semDigest = "sha256:current"

	semPublicChat = int64(-1001000000001)
	semClosedChat = int64(-1001000000002)
	semDirectChat = int64(55)
	semBasicChat  = int64(-4242)

	semBaseDate = int64(1_700_000_000)
)

// fakeQueryEmbedder stands in for Ollama: it returns a fixed vector for any query
// and records what it was asked.
type fakeQueryEmbedder struct {
	info     embed.ModelInfo
	infoErr  error
	embedErr error
	vector   []float32
	// block makes both calls wait for the context, like a hung service.
	block bool

	infoCalls  int
	embedCalls int
	texts      []string
}

func (f *fakeQueryEmbedder) Info(ctx context.Context) (embed.ModelInfo, error) {
	f.infoCalls++
	if f.block {
		<-ctx.Done()
		return embed.ModelInfo{}, fmt.Errorf("embed: request stopped: %w", ctx.Err())
	}
	return f.info, f.infoErr
}

func (f *fakeQueryEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.embedCalls++
	f.texts = append(f.texts, texts...)
	if f.block {
		<-ctx.Done()
		return nil, fmt.Errorf("embed: request stopped: %w", ctx.Err())
	}
	if f.embedErr != nil {
		return nil, f.embedErr
	}
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = f.vector
	}
	return out, nil
}

func newFakeQueryEmbedder() *fakeQueryEmbedder {
	return &fakeQueryEmbedder{info: embed.ModelInfo{Name: semModel, Digest: semDigest, Dim: 2}, vector: []float32{1, 0}}
}

type semanticEnv struct {
	d        *daemon
	db       *store.DB
	vectors  *vectors.Store
	embedder *fakeQueryEmbedder
	access   string
}

func semDate(id int) int64 { return semBaseDate + int64(id)*60 }

// newSemanticEnv saves synthetic messages in four chats and indexes some of
// them as fragments with two-dimensional vectors. The query vector of the fake
// embedder is [1 0], so the cosine of a fragment is its first coordinate.
//
//	public chat   1-3    fragment F1 [0.6 0.8], message 3 is deleted
//	              10-12  fragment F2 [1 0], no word of the query in it
//	              20     in no fragment, found by stems only
//	              30-31  fragment F4 [0 1] in forum topic 9
//	              40-41  fragment of an old model digest [1 0]
//	closed chat   1-2    fragment F3 [1 0], the best vector of all
//	direct chat   7      in no fragment
//	basic group   8      in no fragment
func newSemanticEnv(t *testing.T) *semanticEnv {
	t.Helper()
	d, _, db, accessPath := accessTestDaemon(t)
	ctx := context.Background()
	username := "pubgroup"
	closedName := "closedgroup"
	if err := db.UpsertChats(ctx, []store.Chat{
		{ID: semPublicChat, Type: "supergroup", Title: "Public", Username: &username},
		{ID: semClosedChat, Type: "supergroup", Title: "Closed", Username: &closedName},
		{ID: semDirectChat, Type: "user", Title: "Alice"},
		{ID: semBasicChat, Type: "group", Title: "Basic"},
	}); err != nil {
		t.Fatal(err)
	}
	topic := int64(9)
	msg := func(chat int64, id int, text string) store.Message {
		return store.Message{ChatID: chat, ID: id, Date: semDate(id), Text: text, Raw: json.RawMessage(`{}`)}
	}
	inTopic := msg(semPublicChat, 30, "Доставка в теме форума")
	inTopic.TopicID = &topic
	inTopic2 := msg(semPublicChat, 31, "Ответ в теме")
	inTopic2.TopicID = &topic
	if err := db.UpsertMessages(ctx, []store.Message{
		msg(semPublicChat, 1, "Привет, как дела"),
		msg(semPublicChat, 2, "Доставка задерживается"),
		msg(semPublicChat, 3, "Доставка удалена"),
		msg(semPublicChat, 10, "Погода сегодня хорошая"),
		msg(semPublicChat, 11, "Дождь обещали вечером"),
		msg(semPublicChat, 12, "Зонт не забудь"),
		msg(semPublicChat, 20, "Все доставки отменены"),
		inTopic, inTopic2,
		msg(semPublicChat, 40, "Старая заметка"),
		msg(semPublicChat, 41, "Ещё одна старая заметка"),
		msg(semClosedChat, 1, "Секретная доставка"),
		msg(semClosedChat, 2, "Тайный разговор"),
		msg(semDirectChat, 7, "Доставка в личке"),
		msg(semBasicChat, 8, "Доставка в группе"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.MarkMessagesDeleted(ctx, semPublicChat, []int{3}); err != nil {
		t.Fatal(err)
	}

	vs, err := vectors.Open(filepath.Join(t.TempDir(), "vectors.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = vs.Close() })
	frag := func(chat, topicID int64, first, last int, digest string, vec ...float32) vectors.Fragment {
		return vectors.Fragment{FragmentMeta: vectors.FragmentMeta{
			ChatID: chat, TopicID: topicID, FirstID: int64(first), LastID: int64(last),
			StartsAt: semDate(first), EndsAt: semDate(last), Chars: 300, TextHash: "h", Model: semModel, ModelDigest: digest,
		}, Vec: vec}
	}
	if err := vs.Put(ctx, []vectors.Fragment{
		frag(semPublicChat, 0, 1, 3, semDigest, 0.6, 0.8),
		frag(semPublicChat, 0, 10, 12, semDigest, 1, 0),
		frag(semPublicChat, 9, 30, 31, semDigest, 0, 1),
		frag(semPublicChat, 0, 40, 41, "sha256:old", 1, 0),
		frag(semClosedChat, 0, 1, 2, semDigest, 1, 0),
	}); err != nil {
		t.Fatal(err)
	}

	emb := newFakeQueryEmbedder()
	d.vectors, d.embedder = vs, emb
	writeAccess(t, accessPath, fmt.Sprintf(`{"block":[%d]}`, semClosedChat))
	return &semanticEnv{d: d, db: db, vectors: vs, embedder: emb, access: accessPath}
}

func (e *semanticEnv) search(t *testing.T, p rpc.SemanticSearchParams) rpc.SemanticSearchResult {
	t.Helper()
	got, err := e.d.searchSemantic(context.Background(), params(t, p))
	if err != nil {
		t.Fatalf("search.semantic: %v", err)
	}
	return got.(rpc.SemanticSearchResult)
}

// find returns the fragment with the given first id in the public chat.
func find(result rpc.SemanticSearchResult, chat, first int64) *rpc.SemanticFragment {
	for i := range result.Fragments {
		if f := &result.Fragments[i]; f.ChatID == chat && f.FirstID == first {
			return f
		}
	}
	return nil
}

func linkString(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestSemanticSearchFusesThreeLegs(t *testing.T) {
	e := newSemanticEnv(t)
	result := e.search(t, rpc.SemanticSearchParams{Query: "доставка"})
	if result.VectorSkipped != "" {
		t.Fatalf("vector_skipped = %q, the vector leg must have run", result.VectorSkipped)
	}
	if got := e.embedder.texts; !reflect.DeepEqual(got, []string{"доставка"}) {
		t.Errorf("the embedder got %q, want the bare query without a prefix", got)
	}

	type want struct {
		sources []string
		indexed bool
		topic   int64
		last    int64
		link    string
		match   string // "" means a null match_link
		text    string
	}
	wants := map[[2]int64]want{
		// Both word legs and the vector leg. Message 3 is deleted: it is in the
		// interval but not in the text, and does not make the fragment match.
		{semPublicChat, 1}: {[]string{"exact", "stem", "vector"}, true, 0, 3, "https://t.me/pubgroup/1", "https://t.me/pubgroup/2", "Привет, как дела\nДоставка задерживается"},
		// Only the vector leg: nothing of the query in it, no match link.
		{semPublicChat, 10}: {[]string{"vector"}, true, 0, 12, "https://t.me/pubgroup/10", "", "Погода сегодня хорошая\nДождь обещали вечером\nЗонт не забудь"},
		// A forum topic fragment: both links carry the topic.
		{semPublicChat, 30}: {[]string{"exact", "stem", "vector"}, true, 9, 31, "https://t.me/pubgroup/9/30", "https://t.me/pubgroup/9/30", "Доставка в теме форума\nОтвет в теме"},
		// Messages in no fragment are fragments of their own. "доставки" is
		// another form of the query word: the stemmed leg only.
		{semPublicChat, 20}: {[]string{"stem"}, false, 0, 20, "https://t.me/pubgroup/20", "https://t.me/pubgroup/20", "Все доставки отменены"},
		{semDirectChat, 7}:  {[]string{"exact", "stem"}, false, 0, 7, "", "", "Доставка в личке"},
		{semBasicChat, 8}:   {[]string{"exact", "stem"}, false, 0, 8, "", "", "Доставка в группе"},
	}
	if len(result.Fragments) != len(wants) {
		t.Fatalf("got %d fragments, want %d: %s", len(result.Fragments), len(wants), dump(result))
	}
	for key, w := range wants {
		f := find(result, key[0], key[1])
		if f == nil {
			t.Errorf("fragment %v is missing: %s", key, dump(result))
			continue
		}
		if !reflect.DeepEqual(f.Sources, w.sources) || f.Indexed != w.indexed || f.TopicID != w.topic || f.LastID != w.last || f.Text != w.text {
			t.Errorf("fragment %v = sources %v indexed %v topic %d last %d text %q", key, f.Sources, f.Indexed, f.TopicID, f.LastID, f.Text)
		}
		wantLink, wantMatch := w.link, w.match
		if wantLink == "" {
			wantLink = "<nil>"
		}
		if wantMatch == "" {
			wantMatch = "<nil>"
		}
		if linkString(f.Link) != wantLink || linkString(f.MatchLink) != wantMatch {
			t.Errorf("fragment %v links = %s and %s, want %s and %s", key, linkString(f.Link), linkString(f.MatchLink), wantLink, wantMatch)
		}
	}
	if f := find(result, semDirectChat, 7); f.LinkUnavailable != "private_chat" {
		t.Errorf("direct chat reason = %q", f.LinkUnavailable)
	}
	if f := find(result, semBasicChat, 8); f.LinkUnavailable != "basic_group" {
		t.Errorf("basic group reason = %q", f.LinkUnavailable)
	}
	if f := find(result, semPublicChat, 1); f.LinkUnavailable != "" {
		t.Errorf("a chat with links has reason %q", f.LinkUnavailable)
	}

	// Every fragment names its chat, whichever leg found it: a stored fragment
	// found by words and vector, one found by the vector alone (10), and a
	// message in no fragment (20, 7 and 8). A chat without a username has none.
	chatNames := map[int64]struct{ title, username string }{
		semPublicChat: {"Public", "pubgroup"}, semDirectChat: {"Alice", ""}, semBasicChat: {"Basic", ""},
	}
	for _, f := range result.Fragments {
		w := chatNames[f.ChatID]
		if f.ChatTitle != w.title || w.username == "" && f.ChatUsername != nil || w.username != "" && (f.ChatUsername == nil || *f.ChatUsername != w.username) {
			t.Errorf("fragment %d/%d chat = %q, %v, want %q, %q", f.ChatID, f.FirstID, f.ChatTitle, f.ChatUsername, w.title, w.username)
		}
	}
	if raw, _ := json.Marshal(result.Fragments[0]); !strings.Contains(string(raw), `"chat_title":`) || !strings.Contains(string(raw), `"chat_username":`) {
		t.Errorf("the chat fields are not written out: %s", raw)
	}
	// The closed chat's name is not in the answer either.
	if raw, _ := json.Marshal(result); strings.Contains(string(raw), "Closed") || strings.Contains(string(raw), "closedgroup") {
		t.Errorf("the closed chat's name leaked: %s", raw)
	}

	// Times: stored ones for a fragment, the message's own for a single one.
	first := find(result, semPublicChat, 1)
	if first.StartsAt != time.Unix(semDate(1), 0).UTC().Format(time.RFC3339) || first.EndsAt != time.Unix(semDate(3), 0).UTC().Format(time.RFC3339) {
		t.Errorf("fragment times = %s .. %s", first.StartsAt, first.EndsAt)
	}
	single := find(result, semPublicChat, 20)
	if single.StartsAt != single.EndsAt || single.StartsAt != time.Unix(semDate(20), 0).UTC().Format(time.RFC3339) {
		t.Errorf("single message times = %s .. %s", single.StartsAt, single.EndsAt)
	}

	// Best first. A fragment found by three legs outranks one found by a
	// single leg, whatever the bm25 order inside the legs.
	for i := 1; i < len(result.Fragments); i++ {
		if result.Fragments[i].Score > result.Fragments[i-1].Score {
			t.Errorf("fragments are not sorted by score: %s", dump(result))
		}
	}
	for _, f := range result.Fragments[:2] {
		if len(f.Sources) != 3 {
			t.Errorf("the top fragments must be the ones found by all legs: %s", dump(result))
			break
		}
	}
}

func dump(r rpc.SemanticSearchResult) string {
	var b strings.Builder
	for _, f := range r.Fragments {
		fmt.Fprintf(&b, "[%d %d-%d %v %.5f] ", f.ChatID, f.FirstID, f.LastID, f.Sources, f.Score)
	}
	return b.String()
}

// The closed chat has the best vector and a matching word, yet it is in no leg
// and its text is never read.
func TestSemanticSearchHidesClosedChatInEveryLeg(t *testing.T) {
	e := newSemanticEnv(t)
	for _, query := range []string{"доставка", "секретная", "тайный разговор"} {
		result := e.search(t, rpc.SemanticSearchParams{Query: query})
		raw, _ := json.Marshal(result)
		if find(result, semClosedChat, 1) != nil || find(result, semClosedChat, 2) != nil || strings.Contains(string(raw), "Секретная") || strings.Contains(string(raw), "Тайный") {
			t.Errorf("%q: the closed chat leaked: %s", query, raw)
		}
		for _, f := range result.Fragments {
			if f.ChatID == semClosedChat {
				t.Errorf("%q: fragment of the closed chat: %+v", query, f)
			}
		}
	}
	// Opening the chat in the access list brings it back: the filter, not the
	// data, hid it.
	writeAccess(t, e.access, `{}`)
	result := e.search(t, rpc.SemanticSearchParams{Query: "секретная"})
	if find(result, semClosedChat, 1) == nil {
		t.Errorf("an open chat is not found: %s", dump(result))
	}
}

// The handler double-checks the chat before reading text, which would hide a
// missing filter in the vector leg: so the leg itself is checked here.
func TestVectorLegFiltersClosedChats(t *testing.T) {
	e := newSemanticEnv(t)
	ctx := context.Background()
	chats := func() map[int64]bool {
		leg, err := e.d.vectorLeg(ctx, "q", 10, nil, e.d.policy())
		if err != nil || leg.skipped != "" {
			t.Fatalf("vector leg: %v, skipped %q", err, leg.skipped)
		}
		seen := map[int64]bool{}
		for _, h := range leg.hits {
			seen[h.Meta.ChatID] = true
		}
		return seen
	}
	if seen := chats(); seen[semClosedChat] || !seen[semPublicChat] {
		t.Errorf("chats in the vector leg = %v, want the open chat only", seen)
	}
	writeAccess(t, e.access, `{}`)
	if seen := chats(); !seen[semClosedChat] {
		t.Errorf("an open chat is missing from the vector leg: %v", seen)
	}
}

func TestSemanticSearchLeavesDeletedMessagesOut(t *testing.T) {
	e := newSemanticEnv(t)
	// "удалена" occurs in the deleted message 3 only.
	result := e.search(t, rpc.SemanticSearchParams{Query: "удалена"})
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "удалена") {
		t.Errorf("a deleted message is shown: %s", raw)
	}
	if f := find(result, semPublicChat, 1); f != nil && f.Sources[0] != "vector" {
		t.Errorf("a deleted message made its fragment match: %+v", f)
	}
	// A fragment whose messages are all deleted shows nothing at all.
	if _, err := e.db.MarkMessagesDeleted(context.Background(), semPublicChat, []int{10, 11, 12}); err != nil {
		t.Fatal(err)
	}
	result = e.search(t, rpc.SemanticSearchParams{Query: "доставка"})
	if find(result, semPublicChat, 10) != nil {
		t.Errorf("a fragment of deleted messages is shown: %s", dump(result))
	}
}

// Fragments of another model digest are not mixed in.
func TestSemanticSearchIgnoresFragmentsOfAnotherDigest(t *testing.T) {
	e := newSemanticEnv(t)
	result := e.search(t, rpc.SemanticSearchParams{Query: "доставка"})
	if find(result, semPublicChat, 40) != nil {
		t.Errorf("a fragment of an old digest is in the result: %s", dump(result))
	}
}

func TestSemanticSearchChatLimitAndParams(t *testing.T) {
	e := newSemanticEnv(t)
	chat := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Chat: fmt.Sprint(semPublicChat)})
	if len(chat.Fragments) == 0 {
		t.Fatal("no fragments in the chat")
	}
	for _, f := range chat.Fragments {
		if f.ChatID != semPublicChat {
			t.Errorf("--chat leaked chat %d", f.ChatID)
		}
	}
	// A closed chat answers like an unknown one: empty, and no vector reason.
	closed := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Chat: fmt.Sprint(semClosedChat)})
	if closed.Fragments == nil || len(closed.Fragments) != 0 || closed.VectorSkipped != "" {
		t.Errorf("closed chat: %+v", closed)
	}
	if limited := e.search(t, rpc.SemanticSearchParams{Query: "доставка", Limit: 2}); len(limited.Fragments) != 2 {
		t.Errorf("limit 2 gave %d fragments", len(limited.Fragments))
	}
	for name, p := range map[string]rpc.SemanticSearchParams{
		"empty query":    {Query: "  "},
		"limit too big":  {Query: "доставка", Limit: 101},
		"negative limit": {Query: "доставка", Limit: -1},
	} {
		_, err := e.d.searchSemantic(context.Background(), params(t, p))
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams {
			t.Errorf("%s: err = %v, want invalid params", name, err)
		}
	}
}

// Every reason the vector leg can be skipped for leaves the word legs working
// and names the reason without any text of the query or the messages.
func TestSemanticSearchVectorSkipped(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(e *semanticEnv)
		reason  string
		// embedCalls is how many times Ollama may have been asked to embed.
		embedCalls int
	}{
		{"disabled", func(e *semanticEnv) { e.d.vectors, e.d.embedder = nil, nil }, "disabled", 0},
		{"feature on but vectors.db failed", func(e *semanticEnv) {
			e.d.vectors, e.d.embedder = nil, nil
			e.d.semanticFailure = "vectors file: could not open"
		}, "error", 0},
		{"ollama down", func(e *semanticEnv) {
			e.embedder.infoErr = fmt.Errorf("%w: connection refused", embed.ErrUnavailable)
		}, "unavailable", 0},
		{"model missing", func(e *semanticEnv) {
			e.embedder.infoErr = fmt.Errorf("%w: it is not among the installed models", embed.ErrModelMissing)
		}, "model_missing", 0},
		{"index empty", func(e *semanticEnv) {
			if err := e.vectors.DeleteChats(context.Background(), []int64{semPublicChat, semClosedChat}); err != nil {
				panic(err)
			}
		}, "index_empty", 0},
		{"index of another model only", func(e *semanticEnv) {
			e.embedder.info.Digest = "sha256:newer"
		}, "index_empty", 0},
		{"embed fails", func(e *semanticEnv) { e.embedder.embedErr = errors.New("boom") }, "error", 1},
		{"embed unavailable", func(e *semanticEnv) {
			e.embedder.embedErr = fmt.Errorf("%w: reset", embed.ErrUnavailable)
		}, "unavailable", 1},
		{"bad info", func(e *semanticEnv) { e.embedder.infoErr = embed.ErrBadResponse }, "error", 0},
		{"wrong dimension", func(e *semanticEnv) { e.embedder.vector = []float32{1, 0, 0} }, "error", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newSemanticEnv(t)
			tc.arrange(e)
			result := e.search(t, rpc.SemanticSearchParams{Query: "доставка"})
			if result.VectorSkipped != tc.reason {
				t.Fatalf("vector_skipped = %q, want %q", result.VectorSkipped, tc.reason)
			}
			if e.embedder.embedCalls != tc.embedCalls {
				t.Errorf("Embed called %d times, want %d", e.embedder.embedCalls, tc.embedCalls)
			}
			// The word legs still answer, and no fragment claims the vector leg.
			if find(result, semPublicChat, 20) == nil || find(result, semDirectChat, 7) == nil {
				t.Errorf("no word result: %s", dump(result))
			}
			for _, f := range result.Fragments {
				for _, source := range f.Sources {
					if source == "vector" {
						t.Errorf("a skipped vector leg is a source of %+v", f)
					}
				}
			}
			if find(result, semPublicChat, 10) != nil {
				t.Errorf("the vector-only fragment appeared without a vector leg")
			}
			if strings.Contains(result.VectorSkipped, "доставка") || strings.Contains(result.VectorSkipped, "boom") {
				t.Errorf("the reason leaks text: %q", result.VectorSkipped)
			}
		})
	}
}

// A service that does not answer in time costs the search at most the timeout.
func TestSemanticSearchTimesOutOnAHungOllama(t *testing.T) {
	old := semanticQueryTimeout
	semanticQueryTimeout = 20 * time.Millisecond
	t.Cleanup(func() { semanticQueryTimeout = old })
	e := newSemanticEnv(t)
	e.embedder.block = true
	result := e.search(t, rpc.SemanticSearchParams{Query: "доставка"})
	if result.VectorSkipped != "unavailable" || find(result, semPublicChat, 20) == nil {
		t.Errorf("hung Ollama: skipped %q, fragments %s", result.VectorSkipped, dump(result))
	}
}

// A cancelled request is an error of the request, not a vector reason.
func TestSemanticSearchPropagatesCancellation(t *testing.T) {
	e := newSemanticEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.d.searchSemantic(ctx, params(t, rpc.SemanticSearchParams{Query: "доставка"})); err == nil {
		t.Error("a cancelled request returned no error")
	}
}

func hit(chat, first, last, topic int64) vectors.Hit {
	return vectors.Hit{Key: vectors.Key{ChatID: chat, FirstID: first}, Meta: vectors.FragmentMeta{ChatID: chat, TopicID: topic, FirstID: first, LastID: last, StartsAt: first, EndsAt: last}}
}

func meta(chat, first, last, topic int64) vectors.FragmentMeta {
	return hit(chat, first, last, topic).Meta
}

func wm(chat, id, topic int64) store.WordMatch {
	return store.WordMatch{ChatID: chat, ID: id, TopicID: topic, Date: id}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// The ranks are rigged so every term of the sum is known:
//
//	exact    25 (B), 12 (A), 50 (alone)
//	stemmed  12 (A), 14 (A), 25 (B)
//	vector   C, B
//
// A fragment's rank on a word leg is the best rank of its messages, so message
// 14 adds nothing to A on the stemmed leg.
func TestFuseFragmentsOrderAndScores(t *testing.T) {
	metas := []vectors.FragmentMeta{meta(1, 10, 19, 0), meta(1, 20, 29, 0), meta(1, 30, 39, 0)}
	exact := []store.WordMatch{wm(1, 25, 0), wm(1, 12, 0), wm(1, 50, 0)}
	stemmed := []store.WordMatch{wm(1, 12, 0), wm(1, 14, 0), wm(1, 25, 0)}
	vector := []vectors.Hit{hit(1, 30, 39, 0), hit(1, 20, 29, 0)}
	got := fuseFragments(10, exact, stemmed, vector, metas)

	type want struct {
		first   int64
		score   float64
		sources []string
		indexed bool
		match   int64
	}
	wants := []want{
		{20, 1.0/61 + 1.0/62 + 1.0/63, []string{"exact", "stem", "vector"}, true, 25},
		{10, 1.0/62 + 1.0/61, []string{"exact", "stem"}, true, 12},
		{30, 1.0 / 61, []string{"vector"}, true, 0},
		{50, 1.0 / 63, []string{"exact"}, false, 50},
	}
	if len(got) != len(wants) {
		t.Fatalf("got %d fragments, want %d", len(got), len(wants))
	}
	for i, w := range wants {
		f := got[i]
		if f.ref.firstID != w.first || !approx(f.score, w.score) || !reflect.DeepEqual(f.sources(), w.sources) || f.ref.indexed != w.indexed || f.matchID != w.match {
			t.Errorf("rank %d: first %d score %v sources %v indexed %v match %d; want %+v", i+1, f.ref.firstID, f.score, f.sources(), f.ref.indexed, f.matchID, w)
		}
	}
	if single := got[3].ref; single.firstID != 50 || single.lastID != 50 {
		t.Errorf("a message in no fragment must span itself: %+v", single)
	}
	// The result is cut to the limit.
	if cut := fuseFragments(2, exact, stemmed, vector, metas); len(cut) != 2 || cut[0].ref.firstID != 20 || cut[1].ref.firstID != 10 {
		t.Errorf("limit 2 gave %d fragments", len(cut))
	}
}

// A message in two overlapping fragments belongs to the first by key, so it
// adds one fragment, not two. A message of another topic is outside a fragment
// whose range holds its id.
func TestFuseFragmentsOverlapAndTopics(t *testing.T) {
	metas := []vectors.FragmentMeta{meta(1, 10, 19, 0), meta(1, 15, 24, 0), meta(1, 30, 39, 7)}
	exact := []store.WordMatch{wm(1, 17, 0), wm(1, 18, 0), wm(1, 22, 0), wm(1, 35, 0), wm(1, 36, 7)}
	got := fuseFragments(10, exact, nil, nil, metas)
	var keys [][2]int64
	for _, f := range got {
		keys = append(keys, [2]int64{f.ref.firstID, f.ref.lastID})
	}
	// 17 and 18 share fragment 10-19 (first by key); 22 is only in 15-24; 35 is
	// in no topic-0 fragment, so it stands alone; 36 is in the topic 7 fragment.
	want := [][2]int64{{10, 19}, {15, 24}, {35, 35}, {30, 39}}
	if !reflect.DeepEqual(sortPairs(keys), sortPairs(want)) {
		t.Errorf("fragments = %v, want %v", keys, want)
	}
	for _, f := range got {
		if f.ref.firstID == 10 && f.matchID != 17 {
			t.Errorf("match of the shared fragment = %d, want the better ranked 17", f.matchID)
		}
	}
	// The same fragment found by two legs is one entry.
	two := fuseFragments(10, []store.WordMatch{wm(1, 17, 0)}, nil, []vectors.Hit{hit(1, 10, 19, 0), hit(1, 15, 24, 0)}, metas)
	if len(two) != 2 {
		t.Errorf("overlap gave %d fragments, want 2 (no duplicate)", len(two))
	}
}

func sortPairs(in [][2]int64) [][2]int64 {
	out := append([][2]int64(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j][0] < out[i][0] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
