package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

// semanticFragments runs `tg search --semantic` against the shared link
// fixture (the vector index is off there, so only the word legs answer) and
// returns the decoded fragments by first message id.
func semanticFragments(t *testing.T, e *testEnv, args ...string) (rpc.SemanticSearchResult, map[int64]rpc.SemanticFragment) {
	t.Helper()
	r := e.run(append([]string{"search", "--semantic"}, args...)...)
	if r.code != exitcode.OK {
		t.Fatalf("search --semantic %v: exit %d, %q", args, r.code, r.stdout)
	}
	var data rpc.SemanticSearchResult
	if err := json.Unmarshal(parse(t, r).Data, &data); err != nil {
		t.Fatal(err)
	}
	byID := map[int64]rpc.SemanticFragment{}
	for _, f := range data.Fragments {
		byID[f.FirstID] = f
	}
	return data, byID
}

func TestSearchSemanticAnswersFromWordsWhenTheIndexIsOff(t *testing.T) {
	e := searchLinkEnv(t)
	data, byID := semanticFragments(t, e, "доставки")
	if data.VectorSkipped != "disabled" {
		t.Errorf("vector_skipped = %q, want disabled", data.VectorSkipped)
	}
	if len(byID) != 5 {
		t.Fatalf("got %d fragments, want one per message: %+v", len(byID), data.Fragments)
	}
	wants := map[int64]struct{ link, unavailable, title, username string }{
		1: {"https://t.me/pubgroup/1", "", "Public group", "pubgroup"},
		2: {"https://t.me/c/1000000002/2", "", "Private channel", ""},
		3: {"", "private_chat", "Alice", ""},
		4: {"", "basic_group", "Basic group", ""},
		5: {"https://t.me/pubgroup/9/5", "", "Public group", "pubgroup"},
	}
	for id, w := range wants {
		f := byID[id]
		if f.Indexed || f.LastID != id || f.Text != "Доставка задерживается" || strings.Join(f.Sources, ",") != "stem" {
			t.Errorf("fragment %d = %+v", id, f)
		}
		if w.link == "" && f.Link != nil || w.link != "" && (f.Link == nil || *f.Link != w.link) || f.LinkUnavailable != w.unavailable {
			t.Errorf("fragment %d link = %v (%q), want %q (%q)", id, f.Link, f.LinkUnavailable, w.link, w.unavailable)
		}
		if f.ChatTitle != w.title || w.username == "" && f.ChatUsername != nil || w.username != "" && (f.ChatUsername == nil || *f.ChatUsername != w.username) {
			t.Errorf("fragment %d chat = %q, %v, want %q, %q", id, f.ChatTitle, f.ChatUsername, w.title, w.username)
		}
		// The best message is the only message.
		if (f.Link == nil) != (f.MatchLink == nil) || f.Link != nil && *f.MatchLink != *f.Link {
			t.Errorf("fragment %d match_link = %v, link = %v", id, f.MatchLink, f.Link)
		}
	}
	// The envelope keeps both link fields even when they are null.
	r := e.run("search", "--semantic", "доставки")
	if !strings.Contains(r.stdout, `"match_link":null`) || !strings.Contains(r.stdout, `"link":null`) {
		t.Errorf("null links are not written out: %q", r.stdout)
	}
	if !strings.Contains(r.stdout, `"chat_username":null`) || !strings.Contains(r.stdout, `"chat_title":"Alice"`) {
		t.Errorf("the chat name is not written out: %q", r.stdout)
	}
}

func TestSearchSemanticChatAndLimit(t *testing.T) {
	e := searchLinkEnv(t)
	_, byID := semanticFragments(t, e, "доставки", "--chat", "@pubgroup")
	if len(byID) != 2 || byID[1].ChatID != -1001000000001 || byID[1].TopicID != 0 || byID[5].TopicID != 9 {
		t.Errorf("--chat @pubgroup gave %+v", byID)
	}
	if data, _ := semanticFragments(t, e, "доставки", "--limit", "2"); len(data.Fragments) != 2 {
		t.Errorf("--limit 2 gave %d fragments", len(data.Fragments))
	}
	// A chat the database does not know answers empty, not with an error.
	data, _ := semanticFragments(t, e, "доставки", "--chat", "@unknownname")
	if data.Fragments == nil || len(data.Fragments) != 0 {
		t.Errorf("unknown chat gave %+v", data)
	}
}

func TestSearchSemanticRefusesWhatItCannotDo(t *testing.T) {
	e := searchLinkEnv(t)
	for _, args := range [][]string{
		{"--scope", "chats"},
		{"--scope", "messages,notes"},
		{"--scope", "messages", "--scope", "people"},
		{"--author", "ai"},
		{"--deleted"},
		{"--limit", "101"},
	} {
		r := e.run(append([]string{"search", "--semantic", "доставки"}, args...)...)
		if r.code != exitcode.Usage {
			t.Errorf("%v: exit %d, want %d: %q", args, r.code, exitcode.Usage, r.stdout)
		}
	}
	// The messages scope alone is what --semantic searches.
	if r := e.run("search", "--semantic", "доставки", "--scope", "messages"); r.code != exitcode.OK {
		t.Errorf("--scope messages: exit %d, %q", r.code, r.stdout)
	}
	// Without the flag nothing changed: the ordinary search still answers hits.
	r := e.run("search", "доставки", "--scope", "messages")
	if r.code != exitcode.OK || !strings.Contains(r.stdout, `"hits"`) || strings.Contains(r.stdout, "fragments") || strings.Contains(r.stdout, "vector_skipped") {
		t.Errorf("ordinary search changed: exit %d, %q", r.code, r.stdout)
	}
}

func TestSearchSemanticJSONLAndHuman(t *testing.T) {
	e := searchLinkEnv(t)
	jsonl := e.run("search", "--semantic", "доставки", "--format", "jsonl")
	if jsonl.code != exitcode.OK {
		t.Fatalf("jsonl: exit %d, %q", jsonl.code, jsonl.stdout)
	}
	lines := 0
	for _, line := range strings.Split(strings.TrimSpace(jsonl.stdout), "\n") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		if _, ok := fields["first_id"]; ok {
			lines++
			if _, ok := fields["sources"]; !ok {
				t.Errorf("a fragment line lacks sources: %s", line)
			}
		}
	}
	if lines != 5 {
		t.Errorf("jsonl printed %d fragment lines, want 5: %q", lines, jsonl.stdout)
	}

	human := e.run("search", "--semantic", "доставки", "--human")
	if human.code != exitcode.OK {
		t.Fatalf("human: exit %d, %q", human.code, human.stdout)
	}
	for _, want := range []string{"untrusted Telegram text", "| Доставка задерживается", "https://t.me/pubgroup/1", "private_chat", "vector_skipped", "disabled", "chat_title", "| Public group", "chat_username", "| pubgroup"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("--human output lacks %q: %q", want, human.stdout)
		}
	}
}

func TestSearchSemanticSchema(t *testing.T) {
	search, ok := schemaCommands(t)["search"]
	if !ok {
		t.Fatal("search is not in the schema")
	}
	var flag *flagDoc
	for i := range search.Flags {
		if search.Flags[i].Name == "semantic" {
			flag = &search.Flags[i]
		}
	}
	if flag == nil || flag.Type != "bool" || !strings.Contains(flag.Usage, "messages only") {
		t.Errorf("semantic flag = %+v", flag)
	}
	var minFlag *flagDoc
	for i := range search.Flags {
		if search.Flags[i].Name == "min-similarity" {
			minFlag = &search.Flags[i]
		}
	}
	if minFlag == nil || minFlag.Type != "float64" || !strings.Contains(minFlag.Usage, "--semantic") {
		t.Errorf("min-similarity flag = %+v", minFlag)
	}
	paths := []string{"hits.link", "fragments.chat_id", "fragments.topic_id", "fragments.first_id", "fragments.last_id", "fragments.starts_at", "fragments.ends_at",
		"fragments.score", "fragments.sources", "fragments.indexed", "fragments.text", "fragments.link", "fragments.match_link", "fragments.link_unavailable", "fragments.chat_title", "fragments.chat_username", "vector_skipped", "vector_dropped"}
	if missing := schema.Missing(paths, search.Fields); len(missing) != 0 {
		t.Errorf("schema lacks %v", missing)
	}
	// The text and the chat name are other people's text; the rest is ours.
	untrusted := map[string]bool{"fragments.text": true, "fragments.chat_title": true, "fragments.chat_username": true}
	for _, field := range search.Fields {
		switch {
		case untrusted[field.Path] && !field.Untrusted:
			t.Errorf("%s must be marked untrusted", field.Path)
		case strings.HasPrefix(field.Path, "fragments.") && !untrusted[field.Path] && field.Untrusted:
			t.Errorf("%s must not be untrusted", field.Path)
		}
	}
}

func TestSearchSemanticMinSimilarityFlag(t *testing.T) {
	e := searchLinkEnv(t)
	// A valid value, the bounds included, reaches the daemon; the index is off
	// there, so the answer says so and drops nothing.
	for _, value := range []string{"0", "0.5", "1"} {
		r := e.run("search", "--semantic", "доставки", "--min-similarity", value)
		if r.code != exitcode.OK {
			t.Fatalf("--min-similarity %s: exit %d, %q", value, r.code, r.stdout)
		}
		if !strings.Contains(r.stdout, `"vector_dropped":0`) || !strings.Contains(r.stdout, `"vector_skipped":"disabled"`) {
			t.Errorf("--min-similarity %s: %q", value, r.stdout)
		}
	}
	// A value outside 0 to 1, a word and the flag without --semantic are usage errors.
	for _, args := range [][]string{
		{"--semantic", "--min-similarity", "1.01"},
		{"--semantic", "--min-similarity", "-0.1"},
		{"--semantic", "--min-similarity", "NaN"},
		{"--semantic", "--min-similarity", "high"},
		{"--min-similarity", "0.5"},
		{"--scope", "messages", "--min-similarity", "0.5"},
	} {
		r := e.run(append([]string{"search", "доставки"}, args...)...)
		if r.code != exitcode.Usage {
			t.Errorf("%v: exit %d, want %d: %q", args, r.code, exitcode.Usage, r.stdout)
		}
	}
	// The ordinary search has no such field.
	r := e.run("search", "доставки", "--scope", "messages")
	if strings.Contains(r.stdout, "vector_dropped") {
		t.Errorf("ordinary search shows vector_dropped: %q", r.stdout)
	}
}

func TestWriteVectorDroppedIsForPeopleOnly(t *testing.T) {
	for _, tc := range []struct {
		format  output.Format
		dropped int
		want    string
	}{
		{output.FormatHuman, 7, "vector hits below the similarity threshold: 7\n"},
		{output.FormatHuman, 0, ""},
		{output.FormatJSON, 7, ""},
		{output.FormatJSONL, 7, ""},
	} {
		var b strings.Builder
		if err := writeVectorDropped(&b, outputSettings{format: tc.format}, tc.dropped); err != nil {
			t.Fatal(err)
		}
		if b.String() != tc.want {
			t.Errorf("%v, %d: wrote %q, want %q", tc.format, tc.dropped, b.String(), tc.want)
		}
	}
}
