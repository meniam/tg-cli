package vectors

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Chunking thresholds. The values come from the brain-agent decision
// 20260907_0620 (a fragment is a piece of conversation), tuned there on 1387
// messages and checked here by simulation on the owner's database.
const (
	// PauseGap cuts a fragment when the pause before the next message is at
	// least this long, but only once the buffer holds MinCutChars.
	PauseGap = 30 * time.Minute
	// MinCutChars is the text a buffer must gather before a pause may cut it.
	MinCutChars = 300
	// MaxChars is the fragment ceiling, checked before a message is added.
	MaxChars = 1500
	// OverlapChars is the most text the next fragment repeats from the end of
	// the previous one after a cut by the ceiling.
	OverlapChars = 200
	// IdleGap closes a fragment that never reached MinCutChars.
	IdleGap = 24 * time.Hour
	// MaxSpan is the longest time a fragment may cover.
	MaxSpan = 7 * 24 * time.Hour
	// MinIndexChars is the smallest fragment that goes into the vector index;
	// shorter ones stay in full-text search only.
	MinIndexChars = 200
)

// Msg is one message that carries text. The caller passes only messages with
// text (not deleted, not service), sorted by (chat_id, date, id). Date is Unix
// seconds, Chars the number of characters in the text, ReplyTo the id of the
// message answered in the same chat, or 0.
type Msg struct {
	ID      int64
	ChatID  int64
	TopicID int64
	Date    int64
	Chars   int
	ReplyTo int64
}

// Span is a fragment: a continuous run of messages of one chat and topic.
// After a cut by the ceiling the next span starts with the tail of the
// previous one, so IDs may repeat across neighbouring spans.
type Span struct {
	ChatID   int64
	TopicID  int64
	FirstID  int64
	LastID   int64
	StartsAt int64
	EndsAt   int64
	Chars    int
	MsgCount int
	IDs      []int64
}

// Indexable reports whether the span is long enough for the vector index.
func (s Span) Indexable() bool { return s.Chars >= MinIndexChars }

// Chunk cuts messages into fragments. The input must be sorted by
// (chat_id, date, id); the result follows the input order. A change of chat or
// topic always closes a fragment. A message longer than MaxChars is never
// split: it forms a fragment of its own.
func Chunk(msgs []Msg) []Span {
	var c chunker
	for _, m := range msgs {
		c.add(m)
	}
	c.emit()
	return c.out
}

type chunker struct {
	out   []Span
	buf   []Msg
	chars int
	ids   map[int64]struct{}
}

func (c *chunker) add(m Msg) {
	if len(c.buf) > 0 {
		last := c.buf[len(c.buf)-1]
		if m.ChatID != last.ChatID || m.TopicID != last.TopicID {
			c.cut(nil)
		}
	}
	if m.Chars > MaxChars {
		c.cut(nil)
		c.out = append(c.out, spanOf([]Msg{m}, m.Chars))
		return
	}
	if len(c.buf) > 0 {
		first, last := c.buf[0], c.buf[len(c.buf)-1]
		gap := m.Date - last.Date
		switch {
		case m.Date-first.Date >= seconds(MaxSpan):
			c.cut(nil)
		case c.chars < MinCutChars && gap >= seconds(IdleGap):
			c.cut(nil)
		case c.chars >= MinCutChars && gap >= seconds(PauseGap) && !c.answers(m):
			c.cut(nil)
		}
	}
	if len(c.buf) > 0 && c.chars+m.Chars > MaxChars {
		tail := c.tail()
		if sumChars(tail)+m.Chars > MaxChars {
			tail = nil
		}
		c.cut(tail)
	}
	c.push(m)
}

// answers reports whether m replies to a message in the buffer.
func (c *chunker) answers(m Msg) bool {
	if m.ReplyTo == 0 {
		return false
	}
	_, ok := c.ids[m.ReplyTo]
	return ok
}

// tail returns the longest suffix of the buffer, without its first message,
// holding at most OverlapChars. Excluding the first message keeps the next
// span's first id different from this span's, so keys never collide. A single
// message longer than the limit gives no overlap.
func (c *chunker) tail() []Msg {
	sum, from := 0, len(c.buf)
	for from > 1 && sum+c.buf[from-1].Chars <= OverlapChars {
		from--
		sum += c.buf[from].Chars
	}
	return c.buf[from:]
}

// cut emits the buffer and starts the next one with the given carried tail.
func (c *chunker) cut(tail []Msg) {
	c.emit()
	carried := append([]Msg(nil), tail...)
	c.buf = carried
	c.chars = sumChars(carried)
	c.ids = make(map[int64]struct{}, len(carried))
	for _, m := range carried {
		c.ids[m.ID] = struct{}{}
	}
}

func (c *chunker) emit() {
	if len(c.buf) == 0 {
		return
	}
	c.out = append(c.out, spanOf(c.buf, c.chars))
	c.buf, c.chars = nil, 0
}

func (c *chunker) push(m Msg) {
	if c.ids == nil {
		c.ids = map[int64]struct{}{}
	}
	c.buf = append(c.buf, m)
	c.chars += m.Chars
	c.ids[m.ID] = struct{}{}
}

func spanOf(msgs []Msg, chars int) Span {
	first, last := msgs[0], msgs[len(msgs)-1]
	ids := make([]int64, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return Span{
		ChatID:   first.ChatID,
		TopicID:  first.TopicID,
		FirstID:  first.ID,
		LastID:   last.ID,
		StartsAt: first.Date,
		EndsAt:   last.Date,
		Chars:    chars,
		MsgCount: len(msgs),
		IDs:      ids,
	}
}

func sumChars(msgs []Msg) int {
	n := 0
	for _, m := range msgs {
		n += m.Chars
	}
	return n
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

// FragmentText joins message texts with a line feed into the text that is
// embedded and hashed.
func FragmentText(texts []string) string { return strings.Join(texts, "\n") }

// TextHash returns the hex SHA-256 of text.
func TextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
