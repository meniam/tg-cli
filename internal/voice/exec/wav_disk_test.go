package exec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

type wavSpec struct {
	format   uint16
	channels uint16
	rate     uint32
	bits     uint16
	// listChunk adds an ffmpeg-style LIST chunk before the data.
	listChunk bool
	// dataField is the data size written into the header; payload is the
	// number of data bytes actually stored.
	dataField uint32
	payload   int
}

func goodWAV(seconds int) wavSpec {
	n := seconds * 16000 * 2
	return wavSpec{format: 1, channels: 1, rate: 16000, bits: 16, dataField: uint32(n), payload: n}
}

func writeWAV(t *testing.T, w wavSpec) string {
	t.Helper()
	var b bytes.Buffer
	le := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	le(uint32(36 + w.payload))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	le(uint32(16))
	le(w.format)
	le(w.channels)
	le(w.rate)
	le(w.rate * uint32(w.channels) * uint32(w.bits) / 8)
	le(w.channels * w.bits / 8)
	le(w.bits)
	if w.listChunk {
		b.WriteString("LIST")
		le(uint32(26))
		b.WriteString("INFOISFT")
		le(uint32(14))
		b.WriteString("Lavf61.7.100\x00\x00")
	}
	b.WriteString("data")
	le(w.dataField)
	b.Write(make([]byte, w.payload))
	p := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecWavSeconds(t *testing.T) {
	cases := []struct {
		name string
		w    wavSpec
		want float64
	}{
		{"three seconds", goodWAV(3), 3},
		{"ffmpeg style with LIST chunk", func() wavSpec { w := goodWAV(2); w.listChunk = true; return w }(), 2},
		{"half second", wavSpec{format: 1, channels: 1, rate: 16000, bits: 16, dataField: 16000, payload: 16000}, 0.5},
		{"streamed output, size 0xFFFFFFFF", wavSpec{format: 1, channels: 1, rate: 16000, bits: 16, dataField: 0xFFFFFFFF, payload: 64000}, 2},
		{"streamed output, size 0", wavSpec{format: 1, channels: 1, rate: 16000, bits: 16, dataField: 0, payload: 32000}, 1},
		{"header larger than the file", wavSpec{format: 1, channels: 1, rate: 16000, bits: 16, dataField: 1 << 30, payload: 32000}, 1},
		{"odd trailing byte is not a sample", wavSpec{format: 1, channels: 1, rate: 16000, bits: 16, dataField: 32001, payload: 32001}, 1},
	}
	for _, tc := range cases {
		got, err := WavSeconds(writeWAV(t, tc.w))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestExecWavSecondsRejectsOtherFormats(t *testing.T) {
	for name, mutate := range map[string]func(*wavSpec){
		"stereo":        func(w *wavSpec) { w.channels = 2 },
		"44.1 kHz":      func(w *wavSpec) { w.rate = 44100 },
		"8 bit":         func(w *wavSpec) { w.bits = 8 },
		"float format":  func(w *wavSpec) { w.format = 3 },
		"no audio data": func(w *wavSpec) { w.payload = 0; w.dataField = 0 },
	} {
		w := goodWAV(1)
		mutate(&w)
		if _, err := WavSeconds(writeWAV(t, w)); !errors.Is(err, ErrBadWAV) {
			t.Errorf("%s: error = %v, want ErrBadWAV", name, err)
		}
	}
}

func TestExecWavSecondsRejectsBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string][]byte{
		"empty":            {},
		"short":            []byte("RIFF"),
		"not riff":         bytes.Repeat([]byte("x"), 64),
		"riff but not wav": append([]byte("RIFF\x00\x00\x00\x00AVI "), make([]byte, 40)...),
		"no data chunk":    append([]byte("RIFF\x00\x00\x00\x00WAVE"), []byte("LIST\x04\x00\x00\x00abcd")...),
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := WavSeconds(p); !errors.Is(err, ErrBadWAV) {
			t.Errorf("%s: error = %v, want ErrBadWAV", name, err)
		}
	}
	if _, err := WavSeconds(filepath.Join(dir, "missing")); err == nil {
		t.Error("missing file accepted")
	}
}

func TestExecCheckDisk(t *testing.T) {
	dir := t.TempDir()
	free, err := freeBytes(dir)
	if err != nil {
		t.Skipf("free space is not available: %v", err)
	}
	if free == 0 {
		t.Skip("volume reports no free space")
	}
	if err := CheckDisk(dir, 0); err != nil {
		t.Errorf("need 0: %v", err)
	}
	if err := CheckDisk(dir, -5); err != nil {
		t.Errorf("negative need: %v", err)
	}
	// The live free space moves while tests write files, so keep a wide margin.
	if err := CheckDisk(dir, int64(free/4)); err != nil {
		t.Errorf("need = free/4: %v", err)
	}
	if err := CheckDisk(dir, int64(free)); !errors.Is(err, ErrDisk) {
		t.Errorf("need = free: error = %v, want ErrDisk", err)
	}
	if err := CheckDisk(dir, math.MaxInt64); !errors.Is(err, ErrDisk) {
		t.Errorf("need MaxInt64: error = %v, want ErrDisk", err)
	}
}

func TestExecEnoughSpaceBoundary(t *testing.T) {
	cases := []struct {
		free uint64
		need int64
		ok   bool
	}{
		{2000, 1000, true},  // exactly twice the need fits
		{1999, 1000, false}, // one byte less does not
		{0, 0, true},
		{5, -1, true},
		{math.MaxUint64, math.MaxInt64, true}, // 2 x MaxInt64 still fits in uint64
		{1 << 62, math.MaxInt64, false},
	}
	for _, tc := range cases {
		err := enoughSpace(tc.free, tc.need)
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrDisk)) {
			t.Errorf("enoughSpace(%d, %d) = %v, want ok=%v", tc.free, tc.need, err, tc.ok)
		}
	}
}

func TestExecCheckDiskMissingDirectory(t *testing.T) {
	err := CheckDisk(filepath.Join(t.TempDir(), "no", "such"), 1)
	if err == nil || errors.Is(err, ErrDisk) {
		t.Fatalf("error = %v, want a statfs failure that is not ErrDisk", err)
	}
}
