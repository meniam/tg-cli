package cli

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRenderQRPlain(t *testing.T) {
	var b bytes.Buffer
	if err := renderQR(&b, "tg://login?token=abc", false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	if len(lines) < 10 {
		t.Fatalf("a QR code needs more lines than %d", len(lines))
	}
	width := utf8.RuneCountInString(lines[0])
	for i, line := range lines {
		if utf8.RuneCountInString(line) != width {
			t.Errorf("line %d has %d columns, want %d", i, utf8.RuneCountInString(line), width)
		}
		for _, r := range line {
			if !strings.ContainsRune(" \u2580\u2584\u2588", r) {
				t.Fatalf("line %d has an unexpected character %q", i, r)
			}
		}
	}
	// The quiet zone: the first and the last columns are blank.
	for i, line := range lines {
		rs := []rune(line)
		if rs[0] != ' ' || rs[len(rs)-1] != ' ' {
			t.Errorf("line %d has no quiet zone on the sides", i)
		}
	}
}

func TestRenderQRColor(t *testing.T) {
	var b bytes.Buffer
	if err := renderQR(&b, "tg://login?token=abc", true); err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if !strings.HasSuffix(line, "\x1b[0m") {
			t.Errorf("line %d does not reset the colors at its end", i)
		}
	}
}

func TestRenderQRIsDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	_ = renderQR(&a, "tg://login?token=same", false)
	_ = renderQR(&b, "tg://login?token=same", false)
	if a.String() != b.String() {
		t.Error("the same text must give the same picture")
	}
	var c bytes.Buffer
	_ = renderQR(&c, "tg://login?token=other", false)
	if a.String() == c.String() {
		t.Error("different texts must give different pictures")
	}
}
