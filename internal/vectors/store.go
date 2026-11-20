// Package vectors keeps message-fragment embeddings in their own SQLite file
// (vectors.db) and searches them by exhaustive scan.
//
// The file is derived data: it holds no message text, only the fragment key,
// its interval, a hash of the embedded text and the vector. Deleting the file
// loses nothing that cannot be rebuilt from the message store.
//
// The package is self-contained. It does not import the message store, the
// daemon or the configuration; callers pass what it needs as parameters.
package vectors

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SchemaVersion is the newest vectors.db schema this package knows.
const SchemaVersion = 1

// schema holds one script per version: schema[i] upgrades version i to i+1.
// The primary key (chat_id, first_id) also serves lookups by chat_id, so no
// separate chat_id index exists.
var schema = []string{
	`CREATE TABLE fragments (
	chat_id      INTEGER NOT NULL,
	topic_id     INTEGER NOT NULL DEFAULT 0,
	first_id     INTEGER NOT NULL,
	last_id      INTEGER NOT NULL,
	starts_at    INTEGER NOT NULL,
	ends_at      INTEGER NOT NULL,
	chars        INTEGER NOT NULL,
	text_hash    TEXT    NOT NULL,
	model        TEXT    NOT NULL,
	model_digest TEXT    NOT NULL,
	dim          INTEGER NOT NULL,
	vec          BLOB    NOT NULL,
	embedded_at  INTEGER NOT NULL,
	PRIMARY KEY (chat_id, first_id)
);
CREATE INDEX fragments_model ON fragments (model, model_digest);`,
}

var (
	// ErrDimension reports a vector whose length disagrees with the vectors
	// already stored for the same model and digest, or with the query.
	ErrDimension = errors.New("vector dimension mismatch")
	// ErrBadVector reports an empty, zero-norm, NaN or infinite vector.
	ErrBadVector = errors.New("vector cannot be normalized")
)

// Key identifies a fragment: the chat and the id of its first message.
type Key struct {
	ChatID  int64
	FirstID int64
}

// FragmentMeta describes a stored fragment without its vector or text.
type FragmentMeta struct {
	ChatID      int64
	TopicID     int64
	FirstID     int64
	LastID      int64
	StartsAt    int64
	EndsAt      int64
	Chars       int64
	TextHash    string
	Model       string
	ModelDigest string
	Dim         int
	EmbeddedAt  int64
}

// Key returns the fragment key.
func (m FragmentMeta) Key() Key { return Key{ChatID: m.ChatID, FirstID: m.FirstID} }

// Fragment is a fragment to store. Vec is normalized by Put; Dim is taken from
// len(Vec). A zero EmbeddedAt means "now".
type Fragment struct {
	FragmentMeta
	Vec []float32
}

// Stats summarizes the file.
type Stats struct {
	// Rows is the number of stored fragments.
	Rows int64
	// Bytes is the logical database size (page count times page size),
	// which includes pages still held in the write-ahead log.
	Bytes int64
}

// Store is the vectors.db handle. It is safe for concurrent use.
type Store struct {
	path  string
	write *sql.DB
	read  *sql.DB
	now   func() time.Time

	closeOnce sync.Once
	closeErr  error

	// mu guards groups. A group is immutable once built; a write drops the
	// whole map. Loading happens under the exclusive lock so a write that
	// commits during a load invalidates the result after it is stored.
	mu     sync.RWMutex
	groups map[modelKey]*group
}

type modelKey struct {
	model  string
	digest string
}

// Open creates or opens the vectors file with permissions 0600 and applies the
// schema. It refuses a file written by a newer version of the program.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("vectors database path is empty")
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	created := err == nil
	if created {
		if err = file.Chmod(0600); err != nil {
			file.Close()
			return nil, fmt.Errorf("set vectors database permissions: %w", err)
		}
		if err = file.Close(); err != nil {
			return nil, fmt.Errorf("close new vectors database: %w", err)
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("create vectors database: %w", err)
	}
	if !created {
		version, err := versionAt(path)
		if err != nil {
			return nil, fmt.Errorf("inspect vectors database version: %w", err)
		}
		if version > SchemaVersion {
			return nil, fmt.Errorf("vectors database is newer than this program: version %d", version)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("set vectors database permissions: %w", err)
		}
	}

	write, err := sql.Open("sqlite", dsn(path, "rw"))
	if err != nil {
		return nil, fmt.Errorf("open vectors write pool: %w", err)
	}
	write.SetMaxOpenConns(1)
	if err := write.Ping(); err != nil {
		write.Close()
		return nil, fmt.Errorf("connect vectors write pool: %w", err)
	}
	var mode string
	if err := write.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		write.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	if mode != "wal" {
		write.Close()
		return nil, fmt.Errorf("enable WAL: got %q", mode)
	}
	if err := applySchema(write); err != nil {
		write.Close()
		return nil, err
	}

	read, err := sql.Open("sqlite", dsn(path, "ro"))
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("open vectors read pool: %w", err)
	}
	read.SetMaxOpenConns(4)
	if err := read.Ping(); err != nil {
		read.Close()
		write.Close()
		return nil, fmt.Errorf("connect vectors read pool: %w", err)
	}
	return &Store{path: path, write: write, read: read, now: time.Now, groups: map[modelKey]*group{}}, nil
}

func dsn(path, mode string) string {
	query := url.Values{"mode": {mode}}
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "synchronous(NORMAL)")
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: query.Encode()}).String()
}

func versionAt(path string) (int, error) {
	db, err := sql.Open("sqlite", dsn(path, "ro"))
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var version int
	err = db.QueryRow(`PRAGMA user_version`).Scan(&version)
	return version, err
}

func applySchema(db *sql.DB) error {
	var current int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&current); err != nil {
		return fmt.Errorf("read vectors database version: %w", err)
	}
	for version := current + 1; version <= len(schema); version++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin vectors schema %d: %w", version, err)
		}
		if _, err := tx.Exec(schema[version-1]); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply vectors schema %d: %w", version, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set vectors schema version %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit vectors schema %d: %w", version, err)
		}
	}
	return nil
}

// Path returns the file path supplied to Open.
func (s *Store) Path() string { return s.path }

// Version returns the applied schema version.
func (s *Store) Version() (int, error) {
	var version int
	err := s.read.QueryRow(`PRAGMA user_version`).Scan(&version)
	return version, err
}

// Close releases both connection pools. Repeated calls return the same result.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.groups = map[modelKey]*group{}
		s.mu.Unlock()
		s.closeErr = errors.Join(s.read.Close(), s.write.Close())
	})
	return s.closeErr
}

// invalidate drops every loaded group; the next search reloads what it needs.
func (s *Store) invalidate() {
	s.mu.Lock()
	s.groups = map[modelKey]*group{}
	s.mu.Unlock()
}

type prepared struct {
	meta FragmentMeta
	blob []byte
}

// Put stores fragments in one transaction, replacing rows with the same key.
// Vectors are normalized to unit L2 length on a copy; the caller's slices are
// not modified. A vector whose length differs from the vectors already stored
// for its model and digest is rejected and nothing is written.
func (s *Store) Put(ctx context.Context, frags []Fragment) error {
	if len(frags) == 0 {
		return nil
	}
	nowUnix := s.now().Unix()
	rows := make([]prepared, len(frags))
	batchDim := map[modelKey]int{}
	for i, f := range frags {
		if f.Model == "" {
			return fmt.Errorf("fragment %d/%d: model is empty", f.ChatID, f.FirstID)
		}
		unit, err := normalized(f.Vec)
		if err != nil {
			return fmt.Errorf("fragment %d/%d: %w", f.ChatID, f.FirstID, err)
		}
		key := modelKey{f.Model, f.ModelDigest}
		if dim, ok := batchDim[key]; ok && dim != len(unit) {
			return fmt.Errorf("fragment %d/%d: %w: %d, batch has %d", f.ChatID, f.FirstID, ErrDimension, len(unit), dim)
		}
		batchDim[key] = len(unit)
		meta := f.FragmentMeta
		meta.Dim = len(unit)
		if meta.EmbeddedAt == 0 {
			meta.EmbeddedAt = nowUnix
		}
		rows[i] = prepared{meta: meta, blob: encodeVec(unit)}
	}

	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin put: %w", err)
	}
	defer tx.Rollback()
	for key, dim := range batchDim {
		var stored int
		err := tx.QueryRowContext(ctx,
			`SELECT dim FROM fragments WHERE model = ? AND model_digest = ? LIMIT 1`, key.model, key.digest).Scan(&stored)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("check stored dimension: %w", err)
		case stored != dim:
			return fmt.Errorf("model %q: %w: %d, stored %d", key.model, ErrDimension, dim, stored)
		}
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO fragments
(chat_id, topic_id, first_id, last_id, starts_at, ends_at, chars, text_hash, model, model_digest, dim, vec, embedded_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (chat_id, first_id) DO UPDATE SET
	topic_id = excluded.topic_id, last_id = excluded.last_id, starts_at = excluded.starts_at,
	ends_at = excluded.ends_at, chars = excluded.chars, text_hash = excluded.text_hash,
	model = excluded.model, model_digest = excluded.model_digest, dim = excluded.dim,
	vec = excluded.vec, embedded_at = excluded.embedded_at`)
	if err != nil {
		return fmt.Errorf("prepare put: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		m := r.meta
		if _, err := stmt.ExecContext(ctx, m.ChatID, m.TopicID, m.FirstID, m.LastID, m.StartsAt, m.EndsAt,
			m.Chars, m.TextHash, m.Model, m.ModelDigest, m.Dim, r.blob, m.EmbeddedAt); err != nil {
			return fmt.Errorf("put fragment %d/%d: %w", m.ChatID, m.FirstID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit put: %w", err)
	}
	s.invalidate()
	return nil
}

// Delete removes one fragment. A missing fragment is not an error.
func (s *Store) Delete(ctx context.Context, chatID, firstID int64) error {
	res, err := s.write.ExecContext(ctx, `DELETE FROM fragments WHERE chat_id = ? AND first_id = ?`, chatID, firstID)
	if err != nil {
		return fmt.Errorf("delete fragment: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil || n > 0 {
		s.invalidate()
	}
	return nil
}

// DeleteChats removes every fragment of the given chats in one transaction.
func (s *Store) DeleteChats(ctx context.Context, chatIDs []int64) error {
	if len(chatIDs) == 0 {
		return nil
	}
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete chats: %w", err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `DELETE FROM fragments WHERE chat_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare delete chats: %w", err)
	}
	defer stmt.Close()
	var removed int64
	for _, id := range chatIDs {
		res, err := stmt.ExecContext(ctx, id)
		if err != nil {
			return fmt.Errorf("delete chat %d: %w", id, err)
		}
		n, _ := res.RowsAffected()
		removed += n
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete chats: %w", err)
	}
	if removed > 0 {
		s.invalidate()
	}
	return nil
}

// List returns the metadata of every fragment, without vectors, ordered by
// (chat_id, first_id). The indexer uses it to reconcile with the message store.
func (s *Store) List(ctx context.Context) ([]FragmentMeta, error) {
	rows, err := s.read.QueryContext(ctx, `SELECT chat_id, topic_id, first_id, last_id, starts_at, ends_at,
chars, text_hash, model, model_digest, dim, embedded_at FROM fragments ORDER BY chat_id, first_id`)
	if err != nil {
		return nil, fmt.Errorf("list fragments: %w", err)
	}
	defer rows.Close()
	var out []FragmentMeta
	for rows.Next() {
		var m FragmentMeta
		if err := rows.Scan(&m.ChatID, &m.TopicID, &m.FirstID, &m.LastID, &m.StartsAt, &m.EndsAt,
			&m.Chars, &m.TextHash, &m.Model, &m.ModelDigest, &m.Dim, &m.EmbeddedAt); err != nil {
			return nil, fmt.Errorf("scan fragment: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list fragments: %w", err)
	}
	return out, nil
}

// Count returns the number of fragments embedded by the given model and digest.
func (s *Store) Count(ctx context.Context, model, digest string) (int64, error) {
	var n int64
	err := s.read.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fragments WHERE model = ? AND model_digest = ?`, model, digest).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count fragments: %w", err)
	}
	return n, nil
}

// Stats returns the row count and the logical file size.
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	if err := s.read.QueryRowContext(ctx, `SELECT COUNT(*) FROM fragments`).Scan(&st.Rows); err != nil {
		return Stats{}, fmt.Errorf("count fragments: %w", err)
	}
	var pages, size int64
	if err := s.read.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		return Stats{}, fmt.Errorf("read page count: %w", err)
	}
	if err := s.read.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&size); err != nil {
		return Stats{}, fmt.Errorf("read page size: %w", err)
	}
	st.Bytes = pages * size
	return st, nil
}

// normalized returns a unit-length copy of v.
func normalized(v []float32) ([]float32, error) {
	if len(v) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrBadVector)
	}
	var sum float64
	for _, x := range v {
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("%w: not finite", ErrBadVector)
		}
		sum += f * f
	}
	norm := math.Sqrt(sum)
	if norm == 0 || math.IsInf(norm, 0) {
		return nil, fmt.Errorf("%w: zero norm", ErrBadVector)
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out, nil
}

func encodeVec(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// decodeVec appends the floats in b to dst.
func decodeVec(dst []float32, b []byte) []float32 {
	for i := 0; i+4 <= len(b); i += 4 {
		dst = append(dst, math.Float32frombits(binary.LittleEndian.Uint32(b[i:])))
	}
	return dst
}
