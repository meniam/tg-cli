package cli

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/rpc"
	"tg-cli/internal/schema"
)

func actionReply(method string, raw json.RawMessage) (any, error) {
	switch method {
	case rpc.MethodMessageReact:
		var p rpc.ReactParams
		_ = json.Unmarshal(raw, &p)
		return rpc.ReactResult{ChatID: 42, ID: p.ID, Emoji: p.Emoji, Removed: p.Remove, DryRun: p.DryRun, Action: "react", Count: 1}, nil
	case rpc.MethodMessagePin:
		var p rpc.PinParams
		_ = json.Unmarshal(raw, &p)
		return rpc.PinResult{ChatID: 42, ID: p.ID, Pinned: true, DryRun: p.DryRun, Action: "pin", Count: 1}, nil
	case rpc.MethodMessageUnpin:
		var p rpc.UnpinParams
		_ = json.Unmarshal(raw, &p)
		return rpc.PinResult{ChatID: 42, ID: p.ID, Pinned: false, DryRun: p.DryRun, Action: "unpin", Count: 1}, nil
	case rpc.MethodMessagePoll:
		var p rpc.PollParams
		_ = json.Unmarshal(raw, &p)
		return rpc.SendResult{ChatID: 42, ID: 11, Date: "2026-11-14T10:00:00Z", DryRun: p.DryRun, Action: "poll", Count: 1, TextLength: len(p.Question)}, nil
	}
	return nil, rpc.NewError(rpc.CodeMethodNotFound, "unexpected method")
}

func TestActionCLICommandsAndDryRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		args   []string
	}{
		{"react", rpc.MethodMessageReact, []string{"message", "react", "42", "7", "👍"}},
		{"react remove", rpc.MethodMessageReact, []string{"message", "react", "42", "7", "👍", "--remove"}},
		{"pin", rpc.MethodMessagePin, []string{"message", "pin", "42", "7"}},
		{"pin silent", rpc.MethodMessagePin, []string{"message", "pin", "42", "7", "--silent"}},
		{"unpin", rpc.MethodMessageUnpin, []string{"message", "unpin", "42", "7"}},
		{"poll", rpc.MethodMessagePoll, []string{"message", "poll", "42", "--question", "Question?", "--option", "first", "--option", "second"}},
		{"poll multiple public", rpc.MethodMessagePoll, []string{"message", "poll", "42", "--question", "Question?", "--option", "first", "--option", "second", "--multiple", "--public"}},
		{"poll quiz", rpc.MethodMessagePoll, []string{"message", "poll", "42", "--question", "Question?", "--option", "first", "--option", "second", "--quiz", "--correct", "2", "--explanation", "Because", "--reply-to", "7", "--topic", "6", "--silent"}},
	} {
		for _, dry := range []bool{false, true} {
			name := tc.name
			if dry {
				name += " dry-run"
			}
			t.Run(name, func(t *testing.T) {
				args := slices.Clone(tc.args)
				if dry {
					args = append(args, "--dry-run")
				}
				r := runWriteCLI(t, args, "", actionReply, nil)
				if r.code != exitcode.OK || r.method != tc.method || !strings.Contains(r.stdout, `"ok":true`) {
					t.Fatalf("result: %+v", r)
				}
				if dry != strings.Contains(r.stdout, `"dry_run":true`) || dry != strings.Contains(r.stderr, "no write request was sent to Telegram") {
					t.Fatalf("dry-run: %+v", r)
				}
				switch tc.method {
				case rpc.MethodMessageReact:
					var p rpc.ReactParams
					if err := json.Unmarshal(r.params, &p); err != nil || p.Chat != "42" || p.ID != 7 || p.Emoji != "👍" || p.Remove != (tc.name == "react remove") || p.DryRun != dry {
						t.Fatalf("react params: %+v %v", p, err)
					}
				case rpc.MethodMessagePin:
					var p rpc.PinParams
					if err := json.Unmarshal(r.params, &p); err != nil || p.ID != 7 || p.Silent != (tc.name == "pin silent") || p.DryRun != dry {
						t.Fatalf("pin params: %+v %v", p, err)
					}
					if !strings.Contains(r.stderr, "visible to all chat participants") || !strings.Contains(r.stderr, "tg message unpin") {
						t.Fatalf("pin hint: %s", r.stderr)
					}
				case rpc.MethodMessageUnpin:
					var p rpc.UnpinParams
					if err := json.Unmarshal(r.params, &p); err != nil || p.ID != 7 || p.DryRun != dry {
						t.Fatalf("unpin params: %+v %v", p, err)
					}
				case rpc.MethodMessagePoll:
					var p rpc.PollParams
					if err := json.Unmarshal(r.params, &p); err != nil || !reflect.DeepEqual(p.Options, []string{"first", "second"}) || p.DryRun != dry {
						t.Fatalf("poll params: %+v %v", p, err)
					}
					if p.Multiple != (tc.name == "poll multiple public") || p.Public != (tc.name == "poll multiple public") || p.Quiz != (tc.name == "poll quiz") {
						t.Fatalf("poll mode: %+v", p)
					}
					if p.Quiz && (p.Correct != 2 || p.Explanation != "Because" || p.ReplyTo != 7 || p.Topic != 6 || !p.Silent) {
						t.Fatalf("quiz params: %+v", p)
					}
				}
			})
		}
	}
	quiet := runWriteCLI(t, []string{"message", "pin", "42", "7", "--quiet", "--dry-run"}, "", actionReply, nil)
	if quiet.code != exitcode.OK || quiet.stderr != "" {
		t.Fatalf("quiet hints: %+v", quiet)
	}
}

func TestActionCLILocalValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"reaction id", []string{"message", "react", "42", "0", "👍"}},
		{"reaction nonnumber", []string{"message", "react", "42", "NaN", "👍"}},
		{"reaction empty", []string{"message", "react", "42", "7", ""}},
		{"reaction whitespace", []string{"message", "react", "42", "7", "a b"}},
		{"pin id", []string{"message", "pin", "42", "0"}},
		{"unpin id", []string{"message", "unpin", "42", "0"}},
		{"question missing", []string{"message", "poll", "42", "--option", "A", "--option", "B"}},
		{"question long", []string{"message", "poll", "42", "--question", strings.Repeat("Я", 256), "--option", "A", "--option", "B"}},
		{"one option", []string{"message", "poll", "42", "--question", "Q", "--option", "A"}},
		{"eleven options", append([]string{"message", "poll", "42", "--question", "Q"}, elevenOptions()...)},
		{"option long", []string{"message", "poll", "42", "--question", "Q", "--option", strings.Repeat("Я", 101), "--option", "B"}},
		{"option blank", []string{"message", "poll", "42", "--question", "Q", "--option", " ", "--option", "B"}},
		{"duplicate", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", " A "}},
		{"explanation long", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", "B", "--quiz", "--correct", "1", "--explanation", strings.Repeat("Я", 201)}},
		{"explanation line feeds", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", "B", "--quiz", "--correct", "1", "--explanation", "a\nb\nc\nd"}},
		{"quiz no correct", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", "B", "--quiz"}},
		{"correct outside range", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", "B", "--quiz", "--correct", "3"}},
		{"quiz multiple", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", "B", "--quiz", "--correct", "1", "--multiple"}},
		{"correct without quiz", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", "B", "--correct", "0"}},
		{"explanation without quiz", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", "B", "--explanation", ""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runWriteCLI(t, tc.args, "", actionReply, nil)
			if r.code != exitcode.Usage || r.method != "" {
				t.Fatalf("validation reached daemon: %+v", r)
			}
		})
	}
}

func elevenOptions() []string {
	args := make([]string, 0, 22)
	for i := 0; i < 11; i++ {
		args = append(args, "--option", string(rune('A'+i)))
	}
	return args
}

func TestActionCLIPollAcceptedBoundary(t *testing.T) {
	args := []string{"message", "poll", "42", "--question", strings.Repeat("Я", 255), "--option", strings.Repeat("Я", 100), "--quiz", "--correct", "10", "--explanation", strings.Repeat("Я", 198) + "\n\n"}
	for i := 1; i < 10; i++ {
		args = append(args, "--option", string(rune('A'+i)))
	}
	r := runWriteCLI(t, args, "", actionReply, nil)
	if r.code != exitcode.OK || r.method != rpc.MethodMessagePoll {
		t.Fatalf("boundary: %+v", r)
	}
	var p rpc.PollParams
	if err := json.Unmarshal(r.params, &p); err != nil || len(p.Options) != 10 || len([]rune(p.Question)) != 255 || p.Correct != 10 {
		t.Fatalf("boundary params: %+v %v", p, err)
	}
}

func TestActionCLIFormatsErrorsAndSchema(t *testing.T) {
	commands := schemaCommands(t)
	for _, tc := range []struct {
		name, method, result string
		args                 []string
	}{
		{"message react", rpc.MethodMessageReact, "rpc.ReactResult", []string{"message", "react", "42", "7", "👍"}},
		{"message pin", rpc.MethodMessagePin, "rpc.PinResult", []string{"message", "pin", "42", "7"}},
		{"message unpin", rpc.MethodMessageUnpin, "rpc.PinResult", []string{"message", "unpin", "42", "7"}},
		{"message poll", rpc.MethodMessagePoll, "rpc.SendResult", []string{"message", "poll", "42", "--question", "Q", "--option", "A", "--option", "B"}},
	} {
		cmd, ok := commands[tc.name]
		if !ok || cmd.Safety != "account_write" || cmd.Dangerous || cmd.Result != tc.result || len(cmd.Formats) != 4 || !slices.Contains(cmd.Formats, "jsonl") {
			t.Fatalf("schema %s: %+v", tc.name, cmd)
		}
		if hasFlag(cmd.Flags, "yes") {
			t.Errorf("%s unexpectedly requires --yes", tc.name)
		}
		for _, name := range []string{"dry-run"} {
			if !hasFlag(cmd.Flags, name) {
				t.Errorf("%s missing %s", tc.name, name)
			}
		}
		if tc.name == "message poll" {
			for _, name := range []string{"question", "multiple", "public", "quiz", "correct", "explanation", "reply-to", "topic", "silent"} {
				if !hasFlag(cmd.Flags, name) {
					t.Errorf("poll missing %s", name)
				}
			}
			found := false
			for _, f := range cmd.Flags {
				if f.Name == "option" {
					found = f.Repeatable && f.Type == "stringArray"
				}
			}
			if !found {
				t.Errorf("option is not documented as repeatable stringArray: %+v", cmd.Flags)
			}
		}
		base := runWriteCLI(t, tc.args, "", actionReply, nil)
		if base.code != exitcode.OK || base.method != tc.method {
			t.Fatalf("%s response: %+v", tc.name, base)
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(base.stdout), &envelope); err != nil {
			t.Fatal(err)
		}
		paths, err := schema.Paths(envelope.Data)
		if err != nil || len(schema.Missing(paths, cmd.Fields)) != 0 {
			t.Errorf("schema fields %s: paths=%v missing=%v err=%v", tc.name, paths, schema.Missing(paths, cmd.Fields), err)
		}
		for _, format := range []string{"json", "jsonl", "toon", "human"} {
			args := append(slices.Clone(tc.args), "--format", format)
			r := runWriteCLI(t, args, "", actionReply, nil)
			if r.code != exitcode.OK || r.method != tc.method || strings.TrimSpace(r.stdout) == "" {
				t.Errorf("%s %s: %+v", tc.name, format, r)
			}
			if format == "jsonl" && (!strings.HasPrefix(r.stdout, `{"ok":true`) || strings.Count(strings.TrimSpace(r.stdout), "\n") != 0) {
				t.Errorf("%s jsonl envelope: %q", tc.name, r.stdout)
			}
		}
		selected := runWriteCLI(t, append(slices.Clone(tc.args), "--fields", "chat_id"), "", actionReply, nil)
		if selected.code != exitcode.OK || !strings.Contains(selected.stdout, `"chat_id":42`) || strings.Contains(selected.stdout, `"action"`) {
			t.Errorf("%s --fields: %+v", tc.name, selected)
		}
		jsonlSelected := runWriteCLI(t, append(slices.Clone(tc.args), "--format", "jsonl", "--fields", "chat_id"), "", actionReply, nil)
		if jsonlSelected.code != exitcode.OK || !strings.Contains(jsonlSelected.stdout, `"chat_id":42`) || strings.Contains(jsonlSelected.stdout, `"action"`) {
			t.Errorf("%s jsonl --fields: %+v", tc.name, jsonlSelected)
		}
	}
	for _, tc := range []struct {
		err  error
		code int
	}{
		{rpc.ErrChatNotFound(), exitcode.NotFound},
		{rpc.ErrWriteDisabled(), exitcode.WriteDisabled},
		{rpc.NewError(rpc.CodeTelegram, "chat admin rights are required"), exitcode.Telegram},
		{rpc.NewError(rpc.CodeFloodWait, "FLOOD_WAIT"), exitcode.FloodWait},
		{rpc.NewError(rpc.CodeInvalidParams, "reaction is not available"), exitcode.Usage},
	} {
		r := runWriteCLI(t, []string{"message", "react", "42", "7", "👍"}, "", func(string, json.RawMessage) (any, error) { return nil, tc.err }, nil)
		if r.code != tc.code {
			t.Errorf("exit=%d want=%d: %+v", r.code, tc.code, r)
		}
	}
}

func hasFlag(flags []flagDoc, name string) bool {
	for _, f := range flags {
		if f.Name == name {
			return true
		}
	}
	return false
}

func TestActionCLINegativeChatID(t *testing.T) {
	for _, args := range [][]string{
		{"message", "react", "--remove", "--", "-1001234567890", "7", "👍"},
		{"message", "pin", "--silent", "--", "-1001234567890", "7"},
		{"message", "unpin", "--", "-1001234567890", "7"},
		{"message", "poll", "--question", "Q", "--option", "A", "--option", "B", "--", "-1001234567890"},
	} {
		r := runWriteCLI(t, args, "", actionReply, nil)
		if r.code != exitcode.OK || !strings.Contains(string(r.params), `"chat":"-1001234567890"`) {
			t.Errorf("negative chat id: %+v", r)
		}
	}
}
