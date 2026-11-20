package mediameta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 3))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	for _, tc := range []struct {
		name   string
		encode func(*bytes.Buffer) error
	}{
		{"jpeg", func(b *bytes.Buffer) error { return jpeg.Encode(b, img, nil) }},
		{"png", func(b *bytes.Buffer) error { return png.Encode(b, img) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := tc.encode(&b); err != nil {
				t.Fatal(err)
			}
			w, h, err := Image(bytes.NewReader(b.Bytes()), int64(b.Len()))
			if err != nil || w != 2 || h != 3 {
				t.Fatalf("Image() = %d, %d, %v", w, h, err)
			}
		})
	}
	for _, data := range [][]byte{nil, []byte("GIF89a..."), []byte("RIFF0000WEBPVP"), []byte{0xff, 0xd8, 0xff}} {
		_, _, err := Image(bytes.NewReader(data), int64(len(data)))
		if !errors.Is(err, ErrNotPhoto) {
			t.Fatalf("Image(%q) = %v", data, err)
		}
	}
}

func box(name string, payload []byte) []byte {
	b := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(b[:4], uint32(len(b)))
	copy(b[4:8], name)
	copy(b[8:], payload)
	return b
}

func largeBox(name string, payload []byte) []byte {
	b := make([]byte, 16+len(payload))
	binary.BigEndian.PutUint32(b[:4], 1)
	copy(b[4:8], name)
	binary.BigEndian.PutUint64(b[8:16], uint64(len(b)))
	copy(b[16:], payload)
	return b
}

func movieHeader(version byte) []byte {
	if version == 0 {
		b := make([]byte, 20)
		binary.BigEndian.PutUint32(b[12:16], 1000)
		binary.BigEndian.PutUint32(b[16:20], 1501)
		return box("mvhd", b)
	}
	b := make([]byte, 32)
	b[0] = 1
	binary.BigEndian.PutUint32(b[20:24], 1000)
	binary.BigEndian.PutUint64(b[24:32], 1501)
	return box("mvhd", b)
}

func trackHeader(version byte) []byte {
	n := 84
	offset := 76
	if version == 1 {
		n, offset = 96, 88
	}
	b := make([]byte, n)
	b[0] = version
	binary.BigEndian.PutUint32(b[offset:offset+4], 640<<16|0xffff)
	binary.BigEndian.PutUint32(b[offset+4:offset+8], 360<<16|0xffff)
	return box("tkhd", b)
}

func mp4File(movie, track []byte, afterMdat, large, sizeZero bool) []byte {
	ftyp := box("ftyp", []byte("isom\x00\x00\x00\x00avc1"))
	hdlr := make([]byte, 12)
	copy(hdlr[8:], "vide")
	trak := box("trak", append(track, box("mdia", box("hdlr", hdlr))...))
	moovPayload := append(movie, trak...)
	moov := box("moov", moovPayload)
	if large {
		moov = largeBox("moov", moovPayload)
	}
	if sizeZero {
		binary.BigEndian.PutUint32(moov[:4], 0)
	}
	if afterMdat {
		return append(append(ftyp, box("mdat", []byte("payload"))...), moov...)
	}
	return append(ftyp, moov...)
}

func trackBox(kind string, version byte) []byte {
	hdlr := make([]byte, 12)
	copy(hdlr[8:], kind)
	return box("trak", append(trackHeader(version), box("mdia", box("hdlr", hdlr))...))
}

func TestVideo(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"version zero", mp4File(movieHeader(0), trackHeader(0), false, false, false)},
		{"moov after mdat", mp4File(movieHeader(0), trackHeader(0), true, false, false)},
		{"version one and largesize", mp4File(movieHeader(1), trackHeader(1), true, true, false)},
		{"size zero moov", mp4File(movieHeader(0), trackHeader(0), true, false, true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, w, h, err := Video(bytes.NewReader(tc.data), int64(len(tc.data)))
			if err != nil || d != 2 || w != 640 || h != 360 {
				t.Fatalf("Video() = %d, %d, %d, %v", d, w, h, err)
			}
		})
	}
	valid := mp4File(movieHeader(0), trackHeader(0), false, false, false)
	movieWithAudioFirst := append(movieHeader(0), trackBox("soun", 0)...)
	movieWithAudioFirst = append(movieWithAudioFirst, trackBox("vide", 0)...)
	audioFirst := append(box("ftyp", []byte("isom\x00\x00\x00\x00avc1")), box("moov", movieWithAudioFirst)...)
	d, w, h, err := Video(bytes.NewReader(audioFirst), int64(len(audioFirst)))
	if err != nil || d != 2 || w != 640 || h != 360 {
		t.Fatalf("audio-first tracks: Video() = %d, %d, %d, %v", d, w, h, err)
	}
	missingMovie := mp4File(nil, trackHeader(0), false, false, false)
	oversized := append(box("ftyp", []byte("isom\x00\x00\x00\x00avc1")), box("mdat", nil)...)
	binary.BigEndian.PutUint32(oversized[len(oversized)-8:], 0xffffffff)
	nested := append([]byte(nil), movieHeader(0)...)
	for range 10 {
		nested = box("moov", nested)
	}
	deep := append(box("ftyp", []byte("isom\x00\x00\x00\x00avc1")), nested...)
	tooMany := append([]byte(nil), valid...)
	for range maxMP4Atoms {
		tooMany = append(tooMany, box("free", nil)...)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"zero", nil, ErrNotVideo},
		{"truncated", valid[:len(valid)-2], ErrInvalidVideo},
		{"missing mvhd", missingMovie, ErrInvalidVideo},
		{"oversized atom", oversized, ErrInvalidVideo},
		{"depth limit", deep, ErrInvalidVideo},
		{"atom count limit", tooMany, ErrInvalidVideo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := Video(bytes.NewReader(tc.data), int64(len(tc.data)))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Video() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func makeOggPage(flags byte, granule uint64, serial uint32, packet []byte) []byte {
	segments := (len(packet) + 254) / 255
	b := make([]byte, 27+segments+len(packet))
	copy(b[:4], "OggS")
	b[5] = flags
	binary.LittleEndian.PutUint64(b[6:14], granule)
	binary.LittleEndian.PutUint32(b[14:18], serial)
	b[26] = byte(segments)
	remaining := len(packet)
	for i := range segments {
		n := min(remaining, 255)
		b[27+i] = byte(n)
		remaining -= n
	}
	copy(b[27+segments:], packet)
	return b
}

func TestVoice(t *testing.T) {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	head[8] = 1
	binary.LittleEndian.PutUint16(head[10:12], 312)
	first := makeOggPage(0x02, 0, 7, head)
	middle := makeOggPage(0, 48000, 7, []byte("sound"))
	last := makeOggPage(0x04, 312+48001, 7, []byte("end"))
	valid := append(append(first, middle...), last...)
	duration, err := Voice(bytes.NewReader(valid), int64(len(valid)))
	if err != nil || duration != 2 {
		t.Fatalf("Voice() = %d, %v", duration, err)
	}
	largeLast := makeOggPage(0x04, 312+96000, 7, make([]byte, 65000))
	largeFile := append(append([]byte(nil), first...), largeLast...)
	duration, err = Voice(bytes.NewReader(largeFile), int64(len(largeFile)))
	if err != nil || duration != 2 {
		t.Fatalf("Voice() with large final page = %d, %v", duration, err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		want error
	}{
		{"zero", nil, ErrNotOpus},
		{"Vorbis", makeOggPage(0x02, 0, 7, append([]byte("\x01vorbis"), make([]byte, 12)...)), ErrNotOpus},
		{"missing OpusHead", makeOggPage(0x02, 0, 7, []byte("not opus at all_____")), ErrNotOpus},
		{"no last page", first, ErrInvalidOpus},
		{"truncated last page", valid[:len(valid)-1], ErrInvalidOpus},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Voice(bytes.NewReader(tc.data), int64(len(tc.data)))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Voice() = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAudio(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		mime string
	}{
		{"ID3 MP3", []byte("ID3\x04\x00\x00"), "audio/mpeg"},
		{"frame MP3", []byte{0xff, 0xfb, 0x90, 0x00}, "audio/mpeg"},
		{"ADTS AAC", []byte{0xff, 0xf1, 0x50, 0x80}, "audio/aac"},
		{"M4A", box("ftyp", []byte("M4A \x00\x00\x00\x00isom")), "audio/mp4"},
		{"generic MP4", box("ftyp", []byte("isom\x00\x00\x00\x00mp41")), ""},
		{"zero", nil, ""},
		{"garbage", []byte("garbage"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mime, err := Audio(bytes.NewReader(tc.data), int64(len(tc.data)))
			if tc.mime == "" {
				if !errors.Is(err, ErrUnknown) {
					t.Fatalf("Audio() = %q, %v", mime, err)
				}
			} else if err != nil || mime != tc.mime {
				t.Fatalf("Audio() = %q, %v", mime, err)
			}
		})
	}
}
