package mediameta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"math"
	"testing"
)

// audit06Corpus returns valid files of every format, the starting points for the
// mutation and fuzz tests below.
func audit06Corpus(t testing.TB) (images, videos, voices [][]byte) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 5))
	var p, j bytes.Buffer
	if err := png.Encode(&p, img); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&j, img, nil); err != nil {
		t.Fatal(err)
	}
	images = [][]byte{p.Bytes(), j.Bytes()}
	videos = [][]byte{
		mp4File(movieHeader(0), trackHeader(0), false, false, false),
		mp4File(movieHeader(0), trackHeader(0), true, false, false),
		mp4File(movieHeader(1), trackHeader(1), true, true, false),
		mp4File(movieHeader(0), trackHeader(0), true, false, true),
	}
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	binary.LittleEndian.PutUint16(head[10:12], 312)
	voices = [][]byte{
		append(makeOggPage(0x02, 0, 7, head), makeOggPage(0x04, 312+48001, 7, []byte("end"))...),
		append(append(makeOggPage(0x02, 0, 7, head), makeOggPage(0, 48000, 7, []byte("sound"))...), makeOggPage(0x04, 312+96000, 7, make([]byte, 300))...),
	}
	return images, videos, voices
}

// audit06ParseAll runs every parser on data and fails when one returns an error
// outside the package's own set, or a value that breaks its contract.
func audit06ParseAll(t testing.TB, data []byte) {
	t.Helper()
	r, n := bytes.NewReader(data), int64(len(data))
	if w, h, err := Image(r, n); err != nil {
		if !errors.Is(err, ErrNotPhoto) {
			t.Fatalf("Image() error %v is not ErrNotPhoto", err)
		}
	} else if w <= 0 || h <= 0 {
		t.Fatalf("Image() = %d, %d without an error", w, h)
	}
	if d, w, h, err := Video(r, n); err != nil {
		if !errors.Is(err, ErrNotVideo) && !errors.Is(err, ErrInvalidVideo) {
			t.Fatalf("Video() error %v is not a package error", err)
		}
	} else if d <= 0 || w <= 0 || h <= 0 {
		t.Fatalf("Video() = %d, %d, %d without an error", d, w, h)
	}
	if d, err := Voice(r, n); err != nil {
		if !errors.Is(err, ErrNotOpus) && !errors.Is(err, ErrInvalidOpus) {
			t.Fatalf("Voice() error %v is not a package error", err)
		}
	} else if d <= 0 {
		t.Fatalf("Voice() = %d without an error", d)
	}
	if _, err := Audio(r, n); err != nil && !errors.Is(err, ErrUnknown) {
		t.Fatalf("Audio() error %v is not ErrUnknown", err)
	}
}

// TestAudit06_MutatedFilesTerminateWithPackageErrors damages valid files of every
// format at every length and byte, so the atom, page and image parsers do run
// past their signature checks. Random bytes never reached them.
func TestAudit06_MutatedFilesTerminateWithPackageErrors(t *testing.T) {
	images, videos, voices := audit06Corpus(t)
	var corpus [][]byte
	corpus = append(append(append(corpus, images...), videos...), voices...)
	for _, file := range corpus {
		audit06ParseAll(t, file)
		for n := range file { // every truncation
			audit06ParseAll(t, file[:n])
		}
		for i := range file { // every byte damaged in three ways
			for _, v := range []byte{0x00, 0x7f, 0xff} {
				mutated := append([]byte(nil), file...)
				mutated[i] = v
				audit06ParseAll(t, mutated)
			}
		}
	}
}

// TestAudit06_MutationsReachTheParsers guards the test above against drifting
// back into inputs that fail the first signature check: damage that keeps the
// signature must still give the parser-level error.
func TestAudit06_MutationsReachTheParsers(t *testing.T) {
	_, videos, voices := audit06Corpus(t)
	for n := len(videos[0]) - 1; n > 28; n-- {
		if _, _, _, err := Video(bytes.NewReader(videos[0][:n]), int64(n)); !errors.Is(err, ErrInvalidVideo) {
			t.Fatalf("a truncated MP4 of %d bytes: %v, want ErrInvalidVideo", n, err)
		}
	}
	for n := len(voices[0]) - 1; n > 50; n-- {
		if _, err := Voice(bytes.NewReader(voices[0][:n]), int64(n)); !errors.Is(err, ErrInvalidOpus) {
			t.Fatalf("a truncated Ogg of %d bytes: %v, want ErrInvalidOpus", n, err)
		}
	}
}

func FuzzAudit06_Video(f *testing.F) {
	_, videos, _ := audit06Corpus(f)
	for _, v := range videos {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, data []byte) { audit06ParseAll(t, data) })
}

func FuzzAudit06_Voice(f *testing.F) {
	_, _, voices := audit06Corpus(f)
	for _, v := range voices {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, data []byte) { audit06ParseAll(t, data) })
}

func FuzzAudit06_Image(f *testing.F) {
	images, _, _ := audit06Corpus(f)
	for _, v := range images {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, data []byte) { audit06ParseAll(t, data) })
}

func FuzzAudit06_Audio(f *testing.F) {
	f.Add([]byte("ID3\x04\x00\x00"))
	f.Add([]byte{0xff, 0xfb, 0x90, 0x00})
	f.Add([]byte{0xff, 0xf1, 0x50, 0x80})
	f.Add(box("ftyp", []byte("M4A \x00\x00\x00\x00isom")))
	f.Fuzz(func(t *testing.T, data []byte) { audit06ParseAll(t, data) })
}

func audit06MP4(ftypPayload []byte, moov ...[]byte) []byte {
	out := box("ftyp", ftypPayload)
	for _, m := range moov {
		out = append(out, m...)
	}
	return out
}

func audit06Movie(parts ...[]byte) []byte {
	var payload []byte
	for _, p := range parts {
		payload = append(payload, p...)
	}
	return box("moov", payload)
}

func TestAudit06_VideoBrands(t *testing.T) {
	movie := audit06Movie(movieHeader(0), trackBox("vide", 0))
	zero := []byte{0, 0, 0, 0}
	accepted := [][]byte{}
	for _, brand := range []string{"isom", "iso2", "iso3", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "M4V "} {
		accepted = append(accepted, append(append([]byte(brand), zero...), "zzzz"...)) // major brand
	}
	accepted = append(accepted, append(append([]byte("zzzz"), zero...), "avc1"...)) // compatible brand
	for _, payload := range accepted {
		data := audit06MP4(payload, movie)
		if d, w, h, err := Video(bytes.NewReader(data), int64(len(data))); err != nil || d != 2 || w != 640 || h != 360 {
			t.Errorf("brand %q: Video() = %d, %d, %d, %v", payload, d, w, h, err)
		}
	}
	rejected := [][]byte{
		append(append([]byte("zzzz"), zero...), "yyyy"...),
		append(append([]byte("qt  "), zero...), "qt  "...),
		append(append([]byte("heic"), zero...), "mif1"...),
		append(append([]byte("zzzz"), "isom"...), "yyyy"...), // the minor version is not a brand
		[]byte("isom"), // no minor version: too short
	}
	for _, payload := range rejected {
		data := audit06MP4(payload, movie)
		if _, _, _, err := Video(bytes.NewReader(data), int64(len(data))); !errors.Is(err, ErrNotVideo) {
			t.Errorf("brand %q: Video() error = %v, want ErrNotVideo", payload, err)
		}
	}
}

func TestAudit06_VideoStructureFailures(t *testing.T) {
	ftyp := []byte("isom\x00\x00\x00\x00avc1")
	movie := audit06Movie(movieHeader(0), trackBox("vide", 0))
	overrun := trackBox("vide", 0)
	binary.BigEndian.PutUint32(overrun[:4], uint32(len(overrun)+8))
	badVersion := movieHeader(1)
	badVersion[8] = 2 // payload starts after the 8-byte atom header
	cases := []struct {
		name string
		data []byte
	}{
		// The second moov is valid on its own, so only the one-movie rule refuses it.
		{"second moov", audit06MP4(ftyp, movie, audit06Movie())},
		{"child atom overruns its parent", audit06MP4(ftyp, audit06Movie(movieHeader(0), overrun), box("free", nil))},
		{"mvhd version above one", audit06MP4(ftyp, audit06Movie(badVersion, trackBox("vide", 0)))},
		{"no video track", audit06MP4(ftyp, audit06Movie(movieHeader(0), trackBox("soun", 0)))},
		{"no tracks at all", audit06MP4(ftyp, audit06Movie(movieHeader(0)))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := Video(bytes.NewReader(tc.data), int64(len(tc.data))); !errors.Is(err, ErrInvalidVideo) {
				t.Fatalf("Video() error = %v, want ErrInvalidVideo", err)
			}
		})
	}
}

func TestAudit06_VoiceGranuleAndSerial(t *testing.T) {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	binary.LittleEndian.PutUint16(head[10:12], 312)
	first := makeOggPage(0x02, 0, 7, head)
	file := func(last []byte) []byte { return append(append([]byte(nil), first...), last...) }
	cases := []struct {
		name string
		last []byte
		want int // seconds; 0 means ErrInvalidOpus
	}{
		{"one sample past the pre-skip", makeOggPage(0x04, 312+1, 7, []byte("e")), 1},
		{"exactly one second", makeOggPage(0x04, 312+48000, 7, []byte("e")), 1},
		{"one sample over a second", makeOggPage(0x04, 312+48001, 7, []byte("e")), 2},
		{"granule equals pre-skip", makeOggPage(0x04, 312, 7, []byte("e")), 0},
		{"granule below pre-skip", makeOggPage(0x04, 100, 7, []byte("e")), 0},
		{"granule zero", makeOggPage(0x04, 0, 7, []byte("e")), 0},
		{"granule max uint64", makeOggPage(0x04, math.MaxUint64, 7, []byte("e")), 0},
		{"foreign serial on the last page", makeOggPage(0x04, 312+48000, 8, []byte("e")), 0},
		{"last page without the end flag", makeOggPage(0x00, 312+48000, 7, []byte("e")), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := file(tc.last)
			got, err := Voice(bytes.NewReader(data), int64(len(data)))
			if tc.want == 0 {
				if !errors.Is(err, ErrInvalidOpus) {
					t.Fatalf("Voice() = %d, %v, want ErrInvalidOpus", got, err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("Voice() = %d, %v, want %d", got, err, tc.want)
			}
		})
	}
}
