package mediameta

import (
	"bytes"
	"io"
)

// Audio recognizes MP3, ADTS AAC, and MP4 audio with an M4A/M4B/M4P brand.
// Generic ISO BMFF brands are ambiguous and return ErrUnknown.
func Audio(r io.ReaderAt, size int64) (mime string, err error) {
	if size < 4 {
		return "", ErrUnknown
	}
	var head [16]byte
	n := int64(len(head))
	if size < n {
		n = size
	}
	if !readExact(r, 0, head[:n]) {
		return "", ErrUnknown
	}
	if bytes.Equal(head[:3], []byte("ID3")) || mp3Frame(head[:4]) {
		return "audio/mpeg", nil
	}
	if adtsFrame(head[:4]) {
		return "audio/aac", nil
	}
	if size >= 16 && bytes.Equal(head[4:8], []byte("ftyp")) {
		boxSize := int64(uint32(head[0])<<24 | uint32(head[1])<<16 | uint32(head[2])<<8 | uint32(head[3]))
		if boxSize >= 16 && boxSize <= size && boxSize <= 4096 && boxSize%4 == 0 {
			var brand [4]byte
			for off := int64(8); off < boxSize; off += 4 {
				if off == 12 {
					continue
				}
				if !readExact(r, off, brand[:]) {
					return "", ErrUnknown
				}
				switch string(brand[:]) {
				case "M4A ", "M4B ", "M4P ":
					return "audio/mp4", nil
				}
			}
		}
	}
	return "", ErrUnknown
}

func mp3Frame(b []byte) bool {
	return len(b) >= 4 && b[0] == 0xff && b[1]&0xe0 == 0xe0 &&
		b[1]&0x18 != 0x08 && b[1]&0x06 != 0 &&
		b[2]&0xf0 != 0 && b[2]&0xf0 != 0xf0 && b[2]&0x0c != 0x0c
}

func adtsFrame(b []byte) bool {
	return len(b) >= 4 && b[0] == 0xff && b[1]&0xf6 == 0xf0 &&
		b[2]&0x3c != 0x3c
}
