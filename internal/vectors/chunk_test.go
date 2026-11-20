package vectors

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

const (
	minute = int64(60)
	hour   = 60 * minute
	day    = 24 * hour
)

// msg builds a message in chat 1, topic 0.
func msg(id, date int64, chars int) Msg {
	return Msg{ID: id, ChatID: 1, Date: date, Chars: chars}
}

func reply(m Msg, to int64) Msg {
	m.ReplyTo = to
	return m
}

func idsOf(spans []Span) [][]int64 {
	out := make([][]int64, len(spans))
	for i, s := range spans {
		out[i] = s.IDs
	}
	return out
}

func wantSpans(t *testing.T, got []Span, want [][]int64) {
	t.Helper()
	if !reflect.DeepEqual(idsOf(got), want) {
		t.Errorf("spans = %v, want %v", idsOf(got), want)
	}
}

func TestChunkEmptyInput(t *testing.T) {
	if got := Chunk(nil); len(got) != 0 {
		t.Errorf("Chunk(nil) = %v", got)
	}
	if got := Chunk([]Msg{}); len(got) != 0 {
		t.Errorf("Chunk(empty) = %v", got)
	}
}

func TestChunkFields(t *testing.T) {
	spans := Chunk([]Msg{
		{ID: 10, ChatID: 7, TopicID: 3, Date: 1000, Chars: 120},
		{ID: 12, ChatID: 7, TopicID: 3, Date: 1060, Chars: 80},
		{ID: 15, ChatID: 7, TopicID: 3, Date: 1100, Chars: 50},
	})
	want := []Span{{
		ChatID: 7, TopicID: 3, FirstID: 10, LastID: 15, StartsAt: 1000, EndsAt: 1100,
		Chars: 250, MsgCount: 3, IDs: []int64{10, 12, 15},
	}}
	if !reflect.DeepEqual(spans, want) {
		t.Errorf("spans = %+v, want %+v", spans, want)
	}
}

func TestChunkPauseNeedsEnoughText(t *testing.T) {
	// 200 characters gathered: a 40 minute pause does not cut.
	wantSpans(t, Chunk([]Msg{msg(1, 0, 100), msg(2, 5*minute, 100), msg(3, 45*minute, 10)}),
		[][]int64{{1, 2, 3}})
	// 300 characters gathered: the same pause cuts.
	wantSpans(t, Chunk([]Msg{msg(1, 0, 150), msg(2, 5*minute, 150), msg(3, 45*minute, 10)}),
		[][]int64{{1, 2}, {3}})
}

func TestChunkPauseBoundary(t *testing.T) {
	enough := msg(1, 0, 300)
	wantSpans(t, Chunk([]Msg{enough, msg(2, 30*minute-1, 10)}), [][]int64{{1, 2}})
	wantSpans(t, Chunk([]Msg{enough, msg(2, 30*minute, 10)}), [][]int64{{1}, {2}})
}

func TestChunkReplyCancelsPauseCut(t *testing.T) {
	first := msg(1, 0, 300)
	late := msg(2, 3*hour, 20)
	wantSpans(t, Chunk([]Msg{first, late}), [][]int64{{1}, {2}})
	wantSpans(t, Chunk([]Msg{first, reply(late, 1)}), [][]int64{{1, 2}})
	// A reply to a message outside the buffer cancels nothing.
	wantSpans(t, Chunk([]Msg{first, reply(late, 99)}), [][]int64{{1}, {2}})
}

func TestChunkIdleSilenceClosesShortFragment(t *testing.T) {
	short := msg(1, 0, 100)
	wantSpans(t, Chunk([]Msg{short, msg(2, 24*hour-1, 100)}), [][]int64{{1, 2}})
	wantSpans(t, Chunk([]Msg{short, msg(2, 24*hour, 100)}), [][]int64{{1}, {2}})
}

func TestChunkCeilingWithOverlap(t *testing.T) {
	// 500+500+150+100 = 1250; the next 300 would give 1550. The tail is the
	// longest suffix up to 200 characters: message 4 (100), since 4+3 is 250.
	spans := Chunk([]Msg{msg(1, 0, 500), msg(2, 10, 500), msg(3, 20, 150), msg(4, 30, 100), msg(5, 40, 300)})
	wantSpans(t, spans, [][]int64{{1, 2, 3, 4}, {4, 5}})
	if spans[0].Chars != 1250 || spans[1].Chars != 400 {
		t.Errorf("chars = %d, %d; want 1250, 400", spans[0].Chars, spans[1].Chars)
	}
	if spans[1].FirstID != 4 || spans[1].StartsAt != 30 {
		t.Errorf("overlap span starts at id %d / %d, want 4 / 30", spans[1].FirstID, spans[1].StartsAt)
	}
}

func TestChunkCeilingIsCheckedBeforeAdding(t *testing.T) {
	// Exactly 1500 fits; 1501 does not.
	wantSpans(t, Chunk([]Msg{msg(1, 0, 700), msg(2, 10, 800)}), [][]int64{{1, 2}})
	wantSpans(t, Chunk([]Msg{msg(1, 0, 700), msg(2, 10, 801)}), [][]int64{{1}, {2}})
}

func TestChunkOverlapTailTakesSeveralMessages(t *testing.T) {
	// Suffix 60+70+60 = 190 fits in 200, the message before it (500) does not.
	spans := Chunk([]Msg{msg(1, 0, 800), msg(2, 1, 500), msg(3, 2, 60), msg(4, 3, 70), msg(5, 4, 60), msg(6, 5, 200)})
	wantSpans(t, spans, [][]int64{{1, 2, 3, 4, 5}, {3, 4, 5, 6}})
}

func TestChunkOverlapNeverRepeatsTheFirstMessage(t *testing.T) {
	// The buffer is one 100 character message; repeating it would give two
	// spans with the same first id.
	wantSpans(t, Chunk([]Msg{msg(1, 0, 100), msg(2, 1, 1450)}), [][]int64{{1}, {2}})
}

func TestChunkOverlapDroppedWhenItBreaksTheCeiling(t *testing.T) {
	// Tail (100) plus the new 1450 is 1550, over the ceiling: no overlap.
	wantSpans(t, Chunk([]Msg{msg(1, 0, 500), msg(2, 1, 500), msg(3, 2, 150), msg(4, 3, 100), msg(5, 4, 1450)}),
		[][]int64{{1, 2, 3, 4}, {5}})
}

func TestChunkTailLongerThanLimitGivesNoOverlap(t *testing.T) {
	wantSpans(t, Chunk([]Msg{msg(1, 0, 600), msg(2, 1, 600), msg(3, 2, 400)}), [][]int64{{1, 2}, {3}})
}

func TestChunkLongMessageIsNotSplit(t *testing.T) {
	spans := Chunk([]Msg{msg(1, 0, 100), msg(2, 1, 2000), msg(3, 2, 100)})
	wantSpans(t, spans, [][]int64{{1}, {2}, {3}})
	if spans[1].Chars != 2000 || !spans[1].Indexable() {
		t.Errorf("long message span = %+v", spans[1])
	}
	// A long message first, last or alone behaves the same.
	wantSpans(t, Chunk([]Msg{msg(1, 0, 1501)}), [][]int64{{1}})
	wantSpans(t, Chunk([]Msg{msg(1, 0, 1500), msg(2, 1, 10)}), [][]int64{{1}, {2}}) // 1510 over the ceiling, no tail
}

func TestChunkMaxSpanSevenDays(t *testing.T) {
	// Each message answers the previous one, so pauses never cut; the 7 day
	// limit is the only thing that does. Messages are 12 hours apart: the 15th
	// is exactly 7 days after the first.
	var msgs []Msg
	for i := int64(0); i < 20; i++ {
		m := msg(i+1, i*12*hour, 100)
		if i > 0 {
			m = reply(m, i)
		}
		msgs = append(msgs, m)
	}
	spans := Chunk(msgs)
	if len(spans) != 2 {
		t.Fatalf("%d spans, want 2: %v", len(spans), idsOf(spans))
	}
	if spans[0].LastID != 14 || spans[1].FirstID != 15 {
		t.Errorf("cut between %d and %d, want between 14 and 15", spans[0].LastID, spans[1].FirstID)
	}
	if spans[0].EndsAt-spans[0].StartsAt >= 7*day {
		t.Errorf("first span covers %d seconds, want less than 7 days", spans[0].EndsAt-spans[0].StartsAt)
	}
}

func TestChunkTopicAndChatChangeAlwaysCut(t *testing.T) {
	spans := Chunk([]Msg{
		{ID: 1, ChatID: 1, TopicID: 0, Date: 0, Chars: 50},
		{ID: 2, ChatID: 1, TopicID: 5, Date: 1, Chars: 50},
		{ID: 3, ChatID: 1, TopicID: 5, Date: 2, Chars: 50},
		{ID: 1, ChatID: 2, TopicID: 5, Date: 3, Chars: 50},
	})
	wantSpans(t, spans, [][]int64{{1}, {2, 3}, {1}})
	if spans[1].TopicID != 5 || spans[2].ChatID != 2 {
		t.Errorf("spans = %+v", spans)
	}
	// A reply never carries a fragment across a topic or chat boundary.
	across := Chunk([]Msg{
		{ID: 1, ChatID: 1, Date: 0, Chars: 400},
		{ID: 2, ChatID: 1, TopicID: 9, Date: 1, Chars: 10, ReplyTo: 1},
	})
	wantSpans(t, across, [][]int64{{1}, {2}})
}

func TestIndexableBoundary(t *testing.T) {
	if (Span{Chars: 199}).Indexable() {
		t.Error("199 characters are indexable")
	}
	if !(Span{Chars: 200}).Indexable() {
		t.Error("200 characters are not indexable")
	}
}

func TestFragmentTextAndHash(t *testing.T) {
	if got := FragmentText([]string{"a", "b", "c"}); got != "a\nb\nc" {
		t.Errorf("FragmentText = %q", got)
	}
	if got := FragmentText(nil); got != "" {
		t.Errorf("FragmentText(nil) = %q", got)
	}
	// SHA-256 of "abc".
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got := TextHash("abc"); got != want {
		t.Errorf("TextHash = %s, want %s", got, want)
	}
	if TextHash("a\nb") == TextHash("a b") {
		t.Error("hash ignores the separator")
	}
}

// randomStream builds a sorted stream of messages over a few chats and topics
// with gaps from seconds to days, replies and the odd very long message.
func randomStream(rng *rand.Rand) []Msg {
	var msgs []Msg
	id := int64(0)
	for chat := int64(1); chat <= 3; chat++ {
		date := int64(1_700_000_000)
		topic := int64(0)
		for n := rng.Intn(150); n > 0; n-- {
			id++
			switch rng.Intn(6) {
			case 0:
				date += rng.Int63n(40 * minute)
			case 1:
				date += rng.Int63n(30 * hour)
			case 2:
				date += rng.Int63n(9 * day)
			default:
				date += rng.Int63n(2 * minute)
			}
			chars := 1 + rng.Intn(120)
			switch rng.Intn(30) {
			case 0:
				chars = 1000 + rng.Intn(1500)
			case 1:
				chars = 200 + rng.Intn(400)
			}
			if rng.Intn(25) == 0 {
				topic = int64(rng.Intn(3))
			}
			m := Msg{ID: id, ChatID: chat, TopicID: topic, Date: date, Chars: chars}
			if id > 1 && rng.Intn(10) < 3 {
				m.ReplyTo = id - 1 - int64(rng.Intn(min(int(id), 6)))
				if m.ReplyTo < 0 {
					m.ReplyTo = 0
				}
			}
			msgs = append(msgs, m)
		}
	}
	return msgs
}

func TestChunkProperties(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			msgs := randomStream(rand.New(rand.NewSource(seed)))
			spans := Chunk(msgs)
			checkChunkProperties(t, msgs, spans)
			if again := Chunk(msgs); !reflect.DeepEqual(spans, again) {
				t.Error("Chunk is not deterministic")
			}
		})
	}
}

func checkChunkProperties(t *testing.T, msgs []Msg, spans []Span) {
	t.Helper()
	byID := map[int64]Msg{}
	for _, m := range msgs {
		byID[m.ID] = m // ids are unique across the stream
	}

	covered := map[int64]bool{}
	for i, s := range spans {
		if s.MsgCount != len(s.IDs) || s.MsgCount == 0 {
			t.Fatalf("span %d: MsgCount %d, %d ids", i, s.MsgCount, len(s.IDs))
		}
		sum := 0
		for j, id := range s.IDs {
			m := byID[id]
			if m.ChatID != s.ChatID || m.TopicID != s.TopicID {
				t.Fatalf("span %d mixes chats or topics: message %d is %d/%d, span %d/%d",
					i, id, m.ChatID, m.TopicID, s.ChatID, s.TopicID)
			}
			if j > 0 && m.Date < byID[s.IDs[j-1]].Date {
				t.Fatalf("span %d: dates go backwards at message %d", i, id)
			}
			sum += m.Chars
			covered[id] = true
		}
		if s.Chars != sum {
			t.Fatalf("span %d: Chars %d, messages sum %d", i, s.Chars, sum)
		}
		if s.FirstID != s.IDs[0] || s.LastID != s.IDs[len(s.IDs)-1] ||
			s.StartsAt != byID[s.FirstID].Date || s.EndsAt != byID[s.LastID].Date {
			t.Fatalf("span %d: interval fields disagree with the messages: %+v", i, s)
		}
		if s.Chars > MaxChars && s.MsgCount != 1 {
			t.Fatalf("span %d has %d chars in %d messages, over the ceiling", i, s.Chars, s.MsgCount)
		}
		if s.EndsAt-s.StartsAt >= seconds(MaxSpan) {
			t.Fatalf("span %d covers %d seconds, 7 days or more", i, s.EndsAt-s.StartsAt)
		}
	}
	for _, m := range msgs {
		if !covered[m.ID] {
			t.Fatalf("message %d is in no span", m.ID)
		}
	}

	// Neighbouring spans of one chat advance and share only a short tail.
	seen := map[Key]bool{}
	for i, s := range spans {
		k := Key{s.ChatID, s.FirstID}
		if seen[k] {
			t.Fatalf("duplicate key %v", k)
		}
		seen[k] = true
		if i == 0 || spans[i-1].ChatID != s.ChatID {
			continue
		}
		prev := spans[i-1]
		if s.FirstID <= prev.FirstID || s.LastID < prev.LastID || s.StartsAt < prev.StartsAt || s.EndsAt < prev.EndsAt {
			t.Fatalf("spans %d and %d of chat %d are not monotonic: %+v then %+v", i-1, i, s.ChatID, prev, s)
		}
		shared := 0
		inPrev := map[int64]bool{}
		for _, id := range prev.IDs {
			inPrev[id] = true
		}
		for _, id := range s.IDs {
			if inPrev[id] {
				shared++
			}
		}
		if shared == 0 {
			continue
		}
		// Overlap means a cut by the ceiling: a suffix of prev, at most 200
		// characters, and the next new message would not have fitted.
		tail := prev.IDs[len(prev.IDs)-shared:]
		if !reflect.DeepEqual(tail, s.IDs[:shared]) {
			t.Fatalf("spans %d and %d: shared messages are not a suffix of the first and a prefix of the second", i-1, i)
		}
		tailChars := 0
		for _, id := range tail {
			tailChars += byID[id].Chars
		}
		if tailChars > OverlapChars {
			t.Fatalf("spans %d and %d share %d characters, over %d", i-1, i, tailChars, OverlapChars)
		}
		if shared == len(s.IDs) {
			t.Fatalf("span %d consists only of the overlap", i)
		}
		if next := byID[s.IDs[shared]]; prev.Chars+next.Chars <= MaxChars {
			t.Fatalf("spans %d and %d overlap although %d + %d fits the ceiling", i-1, i, prev.Chars, next.Chars)
		}
	}
}
