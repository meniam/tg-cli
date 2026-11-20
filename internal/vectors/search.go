package vectors

import (
	"context"
	"errors"
	"fmt"
)

// Index finds the fragments nearest to a query vector. Store implements it by
// exhaustive scan; a later implementation may replace the scan if the corpus
// outgrows it.
type Index interface {
	Search(ctx context.Context, query []float32, k int, opts SearchOptions) ([]Hit, error)
}

var _ Index = (*Store)(nil)

// SearchOptions narrows a search.
type SearchOptions struct {
	// Model and ModelDigest select the rows to compare. Vectors of another
	// model or digest are never mixed in. Model is required.
	Model       string
	ModelDigest string
	// Allow, when set, is asked about each chat before any score is computed;
	// fragments of a chat it rejects are skipped.
	Allow func(chatID int64) bool
	// ChatID, when set, restricts the search to one chat.
	ChatID *int64
}

// Hit is one search result. Score is the cosine similarity of the query and
// the fragment; Meta carries the stored metadata.
type Hit struct {
	Key   Key
	Score float32
	Meta  FragmentMeta
}

// group holds the vectors of one model and digest as a single flat slice.
// Rows are ordered by (chat_id, first_id), so a lower row index is also a
// lower key, which makes tie-breaking by index equal to tie-breaking by key.
type group struct {
	dim   int
	data  []float32 // len(metas)*dim floats, row-major
	metas []FragmentMeta
}

// Search returns up to k fragments with the highest scores, best first. Equal
// scores are ordered by (chat_id, first_id). It is safe to call from several
// goroutines, also while another goroutine writes.
func (s *Store) Search(ctx context.Context, query []float32, k int, opts SearchOptions) ([]Hit, error) {
	if opts.Model == "" {
		return nil, errors.New("search: model is empty")
	}
	q, err := normalized(query)
	if err != nil {
		return nil, fmt.Errorf("search: query: %w", err)
	}
	if k <= 0 {
		return nil, nil
	}
	g, err := s.loadGroup(ctx, modelKey{opts.Model, opts.ModelDigest})
	if err != nil {
		return nil, err
	}
	if len(g.metas) == 0 {
		return nil, nil
	}
	if g.dim != len(q) {
		return nil, fmt.Errorf("search: %w: query has %d, stored %d", ErrDimension, len(q), g.dim)
	}
	return g.top(ctx, q, k, opts)
}

type candidate struct {
	score float32
	row   int
}

func (g *group) top(ctx context.Context, q []float32, k int, opts SearchOptions) ([]Hit, error) {
	best := make([]candidate, 0, min(k, len(g.metas)))
	var (
		lastChat    int64
		lastAllowed bool
		haveLast    bool
	)
	for row := range g.metas {
		if row&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		chat := g.metas[row].ChatID
		if !haveLast || chat != lastChat {
			lastChat, haveLast = chat, true
			lastAllowed = (opts.ChatID == nil || *opts.ChatID == chat) && (opts.Allow == nil || opts.Allow(chat))
		}
		if !lastAllowed {
			continue
		}
		score := dot(q, g.data[row*g.dim:(row+1)*g.dim])
		// Rows arrive in key order, so a score equal to the current worst
		// belongs behind it and cannot enter a full list.
		if len(best) == k && score <= best[k-1].score {
			continue
		}
		at := len(best)
		if at == k {
			at--
		} else {
			best = append(best, candidate{})
		}
		for at > 0 && best[at-1].score < score {
			best[at] = best[at-1]
			at--
		}
		best[at] = candidate{score: score, row: row}
	}
	hits := make([]Hit, len(best))
	for i, c := range best {
		meta := g.metas[c.row]
		hits[i] = Hit{Key: meta.Key(), Score: c.score, Meta: meta}
	}
	return hits, nil
}

// dot returns the scalar product of a and b, which must have equal length.
func dot(a, b []float32) float32 {
	n := len(a)
	b = b[:n]
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= n; i += 4 {
		s0 += a[i] * b[i]
		s1 += a[i+1] * b[i+1]
		s2 += a[i+2] * b[i+2]
		s3 += a[i+3] * b[i+3]
	}
	for ; i < n; i++ {
		s0 += a[i] * b[i]
	}
	return (s0 + s1) + (s2 + s3)
}

// loadGroup returns the loaded group for key, reading it from the file on the
// first use after any write.
func (s *Store) loadGroup(ctx context.Context, key modelKey) (*group, error) {
	s.mu.RLock()
	g, ok := s.groups[key]
	s.mu.RUnlock()
	if ok {
		return g, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if g, ok := s.groups[key]; ok {
		return g, nil
	}
	g, err := s.readGroup(ctx, key)
	if err != nil {
		return nil, err
	}
	s.groups[key] = g
	return g, nil
}

func (s *Store) readGroup(ctx context.Context, key modelKey) (*group, error) {
	var count int
	if err := s.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fragments WHERE model = ? AND model_digest = ?`, key.model, key.digest).Scan(&count); err != nil {
		return nil, fmt.Errorf("load vectors: %w", err)
	}
	g := &group{metas: make([]FragmentMeta, 0, count)}
	if count == 0 {
		return g, nil
	}
	rows, err := s.read.QueryContext(ctx, `SELECT chat_id, topic_id, first_id, last_id, starts_at, ends_at,
chars, text_hash, model, model_digest, dim, embedded_at, vec
FROM fragments WHERE model = ? AND model_digest = ? ORDER BY chat_id, first_id`, key.model, key.digest)
	if err != nil {
		return nil, fmt.Errorf("load vectors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			m    FragmentMeta
			blob []byte
		)
		if err := rows.Scan(&m.ChatID, &m.TopicID, &m.FirstID, &m.LastID, &m.StartsAt, &m.EndsAt,
			&m.Chars, &m.TextHash, &m.Model, &m.ModelDigest, &m.Dim, &m.EmbeddedAt, &blob); err != nil {
			return nil, fmt.Errorf("load vectors: %w", err)
		}
		if g.dim == 0 {
			g.dim = m.Dim
			g.data = make([]float32, 0, count*g.dim)
		}
		if m.Dim != g.dim || len(blob) != 4*g.dim {
			return nil, fmt.Errorf("load vectors: fragment %d/%d is corrupt: dim %d, blob %d bytes, expected dim %d",
				m.ChatID, m.FirstID, m.Dim, len(blob), g.dim)
		}
		g.data = decodeVec(g.data, blob)
		g.metas = append(g.metas, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load vectors: %w", err)
	}
	return g, nil
}
