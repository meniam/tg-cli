package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync"
	"time"

	"tg-cli/internal/access"
	"tg-cli/internal/embed"
	"tg-cli/internal/redact"
	"tg-cli/internal/rpc"
	"tg-cli/internal/store"
	"tg-cli/internal/textnorm"
	"tg-cli/internal/vectors"
)

// semanticQueryTimeout bounds the Ollama work of one search (the model lookup
// and the query embedding together); past it the search goes on with words
// alone. It is a variable only so that a test can shorten it.
var semanticQueryTimeout = 10 * time.Second

const (
	// semanticTextRunes is the longest fragment text of an answer, in characters.
	semanticTextRunes = 1500
	// semanticDefaultLimit and semanticMaxLimit bound the fragments of an answer.
	semanticDefaultLimit = 20
	semanticMaxLimit     = 100
	// semanticLegFactor sets how many candidates each leg contributes: limit times it.
	semanticLegFactor = 3
)

// searchSemantic is the hybrid search over conversation fragments: exact words,
// stemmed words and vector similarity fused by Reciprocal Rank Fusion. It reads
// local data only and never calls Telegram. Without the vector leg (feature off,
// Ollama down, no index) it answers from the word legs and says why.
func (d *daemon) searchSemantic(ctx context.Context, params json.RawMessage) (any, error) {
	var p rpc.SemanticSearchParams
	if err := decodeParams(params, &p); err != nil {
		return nil, err
	}
	if textnorm.Query(p.Query, p.Prefix) == "" {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "empty search query")
	}
	limit := p.Limit
	if limit == 0 {
		limit = semanticDefaultLimit
	}
	if limit < 1 || limit > semanticMaxLimit {
		return nil, rpc.NewError(rpc.CodeInvalidParams, "limit must be between 1 and 100")
	}
	// The request overrides the setting; a hand-built Options has no setting, so
	// its zero means no threshold.
	minSimilarity := d.semanticMinSimilarity()
	if p.MinSimilarity != nil {
		minSimilarity = *p.MinSimilarity
		if math.IsNaN(minSimilarity) || minSimilarity < 0 || minSimilarity > 1 {
			return nil, rpc.NewError(rpc.CodeInvalidParams, "min_similarity must be a number from 0 to 1")
		}
	}
	policy := d.policy()
	chatID, visible, err := d.searchChatScope(ctx, policy, p.Chat)
	if err != nil {
		return nil, err
	}
	if !visible {
		return rpc.SemanticSearchResult{Fragments: []rpc.SemanticFragment{}}, nil
	}
	legSize := limit * semanticLegFactor
	exact, stemmed, err := d.store.WordLegs(ctx, store.WordLegOptions{Query: p.Query, Prefix: p.Prefix, Limit: legSize, ChatID: chatID, ChatFilter: chatFilter(policy)})
	if err != nil {
		return nil, storeError(err)
	}
	leg, err := d.vectorLeg(ctx, p.Query, legSize, chatID, policy, minSimilarity)
	if err != nil {
		return nil, err
	}
	fused := fuseFragments(limit, exact, stemmed, leg.hits, leg.metas)
	result := rpc.SemanticSearchResult{Fragments: make([]rpc.SemanticFragment, 0, len(fused)), VectorSkipped: leg.skipped, VectorDropped: leg.dropped}
	chatIDs := make([]int64, 0, len(fused))
	for _, f := range fused {
		chatIDs = append(chatIDs, f.ref.chatID)
	}
	links, err := d.store.ChatLinks(ctx, chatIDs)
	if err != nil {
		return nil, storeError(err)
	}
	for _, f := range fused {
		// A closed chat cannot get here: every leg filters it. This check keeps
		// the text of a closed chat unread even if a leg ever stopped doing so.
		if !policy.Allowed(f.ref.chatID) {
			continue
		}
		text, err := d.store.FragmentText(ctx, f.ref.chatID, f.ref.topicID, f.ref.firstID, f.ref.lastID, semanticTextRunes)
		if err != nil {
			return nil, storeError(err)
		}
		if text == "" {
			// Every message of the fragment was deleted after it was indexed.
			continue
		}
		result.Fragments = append(result.Fragments, semanticFragment(f, text, links[f.ref.chatID]))
	}
	return result, nil
}

// semanticFragment builds the answer entry of a fused fragment.
func semanticFragment(f *fusedFragment, text string, chat store.ChatLink) rpc.SemanticFragment {
	var topic *int64
	if f.ref.topicID > 0 {
		topic = &f.ref.topicID
	}
	out := rpc.SemanticFragment{
		ChatID: f.ref.chatID, TopicID: f.ref.topicID, FirstID: f.ref.firstID, LastID: f.ref.lastID,
		StartsAt: time.Unix(f.ref.startsAt, 0).UTC().Format(time.RFC3339),
		EndsAt:   time.Unix(f.ref.endsAt, 0).UTC().Format(time.RFC3339),
		Score:    f.score, Sources: f.sources(), Indexed: f.ref.indexed, Text: text,
	}
	out.ChatTitle = chat.Title
	if chat.Username != "" {
		out.ChatUsername = &chat.Username
	}
	out.Link, out.LinkUnavailable = store.MessageLink(f.ref.chatID, f.ref.firstID, chat.Type, chat.Username, topic)
	if f.matchID != 0 {
		out.MatchLink, _ = store.MessageLink(f.ref.chatID, f.matchID, chat.Type, chat.Username, topic)
	}
	return out
}

// vectorResult is what the vector leg hands to the fusion.
type vectorResult struct {
	hits []vectors.Hit
	// metas are the stored fragments of the current model, in key order; the
	// word legs use them to find the fragment of a message.
	metas []vectors.FragmentMeta
	// skipped is the reason the leg did not run, empty if it did.
	skipped string
	// dropped is the number of hits left out for a cosine below the threshold.
	dropped int
}

// vectorLeg embeds the query and finds the nearest fragments. Hits whose cosine
// similarity is below minSimilarity are dropped and counted before the fusion
// (0 keeps every hit); the word legs are not touched. Anything that goes wrong
// on the way, except the cancellation of the request, becomes a reason in
// skipped: the search never fails because of the vector side. A leg that ran
// but lost every hit to the threshold is not skipped.
func (d *daemon) vectorLeg(ctx context.Context, query string, k int, chatID *int64, policy access.Policy, minSimilarity float64) (vectorResult, error) {
	if d.vectors == nil || d.embedder == nil {
		if d.semanticFailure != "" {
			return vectorResult{skipped: rpc.VectorSkippedError}, nil
		}
		return vectorResult{skipped: rpc.VectorSkippedDisabled}, nil
	}
	skip := func(err error) (vectorResult, error) {
		if ctx.Err() != nil {
			return vectorResult{}, ctx.Err()
		}
		reason := vectorSkipReason(err)
		if reason == rpc.VectorSkippedError {
			d.log.Warn("semantic search fell back to words", "err", redact.Text(err.Error()))
		}
		return vectorResult{skipped: reason}, nil
	}
	// An empty file needs no Ollama call to answer.
	stats, err := d.vectors.Stats(ctx)
	if err != nil {
		return skip(err)
	}
	if stats.Rows == 0 {
		return vectorResult{skipped: rpc.VectorSkippedIndexEmpty}, nil
	}
	callCtx, cancel := context.WithTimeout(ctx, semanticQueryTimeout)
	defer cancel()
	info, err := d.modelInfo.get(callCtx, d.embedder)
	if err != nil {
		return skip(err)
	}
	all, err := d.vectors.List(ctx)
	if err != nil {
		return skip(err)
	}
	var metas []vectors.FragmentMeta
	for _, m := range all {
		if m.Model == info.Name && m.ModelDigest == info.Digest {
			metas = append(metas, m)
		}
	}
	if len(metas) == 0 {
		return vectorResult{skipped: rpc.VectorSkippedIndexEmpty}, nil
	}
	embedded, err := d.embedder.Embed(callCtx, []string{query})
	if err != nil {
		// The model may be gone or replaced: look it up again next time.
		d.modelInfo.reset()
		return skip(err)
	}
	if len(embedded) != 1 {
		return skip(errors.New("the embedder returned a wrong number of vectors"))
	}
	options := vectors.SearchOptions{Model: info.Name, ModelDigest: info.Digest, ChatID: chatID}
	if policy.Enabled() {
		options.Allow = policy.Allowed
	}
	hits, err := d.vectors.Search(ctx, embedded[0], k, options)
	if err != nil {
		return skip(err)
	}
	kept := hits
	if minSimilarity > 0 {
		kept = make([]vectors.Hit, 0, len(hits))
		// Scores are float32; compare at that precision so a hit exactly at the
		// threshold stays.
		floor := float32(minSimilarity)
		for _, h := range hits {
			if h.Score >= floor {
				kept = append(kept, h)
			}
		}
	}
	return vectorResult{hits: kept, metas: metas, dropped: len(hits) - len(kept)}, nil
}

// semanticModelInfoTTL is how long a search trusts the model name and digest it
// read from Ollama. Without the cache every search spent two extra Ollama calls
// (the tag list and a probe embedding), about 35 ms of the 59 ms measured. A
// model replaced in Ollama is noticed within this time or at the next failed
// embedding, whichever comes first.
const semanticModelInfoTTL = time.Minute

// modelInfoCache keeps the last successful answer of Embedder.Info. Failures
// are never cached: the next search asks again.
type modelInfoCache struct {
	mu   sync.Mutex
	info embed.ModelInfo
	at   time.Time
	now  func() time.Time // nil means time.Now; a test replaces it
}

func (c *modelInfoCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// get returns the cached answer while it is fresh and asks the embedder
// otherwise. The lock is held during the call, so concurrent searches share one
// lookup; the call is bounded by the context of the caller.
func (c *modelInfoCache) get(ctx context.Context, e embed.Embedder) (embed.ModelInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && c.clock().Sub(c.at) < semanticModelInfoTTL {
		return c.info, nil
	}
	info, err := e.Info(ctx)
	if err != nil {
		c.at = time.Time{}
		return embed.ModelInfo{}, err
	}
	c.info, c.at = info, c.clock()
	return info, nil
}

// reset forgets the cached answer.
func (c *modelInfoCache) reset() {
	c.mu.Lock()
	c.at = time.Time{}
	c.mu.Unlock()
}

// vectorSkipReason classifies a failure of the vector leg. The error text
// stays out of the answer: only the class is told.
func vectorSkipReason(err error) string {
	switch {
	case errors.Is(err, embed.ErrModelMissing):
		return rpc.VectorSkippedModelMissing
	case errors.Is(err, embed.ErrUnavailable), errors.Is(err, context.DeadlineExceeded):
		return rpc.VectorSkippedUnavailable
	}
	return rpc.VectorSkippedError
}

// fragmentRef identifies a fragment of the answer. An indexed one is a stored
// fragment; the other is a message the index does not cover, taken alone.
type fragmentRef struct {
	chatID, topicID  int64
	firstID, lastID  int64
	startsAt, endsAt int64
	indexed          bool
}

func (r fragmentRef) key() [3]int64 { return [3]int64{r.chatID, r.firstID, r.lastID} }

// fusedFragment is a fragment with its fused score and the legs that found it.
type fusedFragment struct {
	ref                    fragmentRef
	score                  float64
	exact, stemmed, vector bool
	// matchID is the message of the fragment the word legs ranked best, 0 if
	// no word leg found the fragment.
	matchID    int64
	matchScore float64
}

// sources lists the legs in a fixed order.
func (f *fusedFragment) sources() []string {
	out := make([]string, 0, 3)
	if f.exact {
		out = append(out, rpc.SourceExact)
	}
	if f.stemmed {
		out = append(out, rpc.SourceStem)
	}
	if f.vector {
		out = append(out, rpc.SourceVector)
	}
	return out
}

// fuseFragments merges the word legs (ranked messages) and the vector leg
// (ranked fragments) by Reciprocal Rank Fusion over fragments, weights 1.
// A message belongs to the first stored fragment (by key) whose id range holds
// it in the same chat and topic; a message in no stored fragment is a fragment
// of its own. A fragment's rank on a word leg is the best rank of its messages,
// so a second message of a fragment adds nothing to that leg. The best message
// of a fragment for match_link is the one with the highest fused score over the
// word legs; the earlier leg and rank win a tie. Equal fragment scores keep the
// order exact, stemmed, vector. The result is cut to limit.
func fuseFragments(limit int, exact, stemmed []store.WordMatch, vector []vectors.Hit, metas []vectors.FragmentMeta) []*fusedFragment {
	byChat := make(map[int64][]vectors.FragmentMeta)
	for _, m := range metas {
		byChat[m.ChatID] = append(byChat[m.ChatID], m)
	}
	locate := func(m store.WordMatch) fragmentRef {
		for _, meta := range byChat[m.ChatID] {
			if meta.FirstID > m.ID {
				break
			}
			if m.ID <= meta.LastID && meta.TopicID == m.TopicID {
				return fragmentRef{chatID: meta.ChatID, topicID: meta.TopicID, firstID: meta.FirstID, lastID: meta.LastID, startsAt: meta.StartsAt, endsAt: meta.EndsAt, indexed: true}
			}
		}
		return fragmentRef{chatID: m.ChatID, topicID: m.TopicID, firstID: m.ID, lastID: m.ID, startsAt: m.Date, endsAt: m.Date}
	}

	byKey := make(map[[3]int64]*fusedFragment)
	var order []*fusedFragment
	entry := func(ref fragmentRef) *fusedFragment {
		f := byKey[ref.key()]
		if f == nil {
			f = &fusedFragment{ref: ref}
			byKey[ref.key()] = f
			order = append(order, f)
		}
		return f
	}
	rrf := func(rank int) float64 { return 1 / float64(store.RRFK+rank+1) }

	// Fused score of each message over both word legs, for the best message.
	type messageKey [2]int64
	messageScore := make(map[messageKey]float64)
	for _, leg := range [][]store.WordMatch{exact, stemmed} {
		for rank, m := range leg {
			messageScore[messageKey{m.ChatID, m.ID}] += rrf(rank)
		}
	}
	for i, leg := range [][]store.WordMatch{exact, stemmed} {
		credited := make(map[[3]int64]bool)
		for rank, m := range leg {
			ref := locate(m)
			f := entry(ref)
			if !credited[ref.key()] {
				credited[ref.key()] = true
				f.score += rrf(rank)
				if i == 0 {
					f.exact = true
				} else {
					f.stemmed = true
				}
			}
			// Strictly greater: on a tie the message seen first stays.
			if score := messageScore[messageKey{m.ChatID, m.ID}]; f.matchID == 0 || score > f.matchScore {
				f.matchID, f.matchScore = m.ID, score
			}
		}
	}
	for rank, hit := range vector {
		meta := hit.Meta
		f := entry(fragmentRef{chatID: meta.ChatID, topicID: meta.TopicID, firstID: meta.FirstID, lastID: meta.LastID, startsAt: meta.StartsAt, endsAt: meta.EndsAt, indexed: true})
		f.score += rrf(rank)
		f.vector = true
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].score > order[j].score })
	if len(order) > limit {
		order = order[:limit]
	}
	return order
}

func (d *daemon) semanticMinSimilarity() float64 {
	minSimilarity := d.opts.SemanticSearch.MinSimilarity
	if value, ok := d.liveSetting("semantic_search.min_similarity"); ok {
		_ = json.Unmarshal(value, &minSimilarity)
	}
	return minSimilarity
}
