package mediameta

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
)

const oggTailScan = 128 << 10 // Two bounded 64 KiB windows cover a maximum-sized Ogg page.

type oggPage struct {
	flags   byte
	granule uint64
	serial  uint32
	end     int64
	data    int64
	packet  int
}

// Voice reads Ogg Opus duration from the final EOS page's granule position.
func Voice(r io.ReaderAt, size int64) (int, error) {
	first, ok := readOggPage(r, 0, size)
	if !ok || first.flags&0x02 == 0 || first.packet < 19 {
		return 0, ErrNotOpus
	}
	var head [19]byte
	if !readExact(r, first.data, head[:]) || !bytes.Equal(head[:8], []byte("OpusHead")) || head[8] == 0 {
		return 0, ErrNotOpus
	}
	preSkip := uint64(binary.LittleEndian.Uint16(head[10:12]))
	start := size - oggTailScan
	if start < 0 {
		start = 0
	}
	var last oggPage
	found := false
	// A page can be at most 65,307 bytes; scanning the last 128 KiB is bounded.
	buf := make([]byte, size-start)
	if !readExact(r, start, buf) {
		return 0, ErrInvalidOpus
	}
	for i := len(buf) - 4; i >= 0; i-- {
		if !bytes.Equal(buf[i:i+4], []byte("OggS")) {
			continue
		}
		page, ok := readOggPage(r, start+int64(i), size)
		if ok && page.end == size && page.serial == first.serial && page.flags&0x04 != 0 {
			last, found = page, true
			break
		}
	}
	if !found || last.granule == math.MaxUint64 || last.granule <= preSkip {
		return 0, ErrInvalidOpus
	}
	samples := last.granule - preSkip
	seconds := samples / 48000
	if samples%48000 != 0 {
		seconds++
	}
	if seconds == 0 || seconds > uint64(math.MaxInt) {
		return 0, ErrInvalidOpus
	}
	return int(seconds), nil
}

func readOggPage(r io.ReaderAt, off, size int64) (oggPage, bool) {
	if off < 0 || size-off < 27 {
		return oggPage{}, false
	}
	var header [27]byte
	if !readExact(r, off, header[:]) || !bytes.Equal(header[:4], []byte("OggS")) || header[4] != 0 {
		return oggPage{}, false
	}
	segmentCount := int(header[26])
	if size-off < int64(27+segmentCount) {
		return oggPage{}, false
	}
	segments := make([]byte, segmentCount)
	if !readExact(r, off+27, segments) {
		return oggPage{}, false
	}
	dataSize, firstPacket := 0, 0
	packetEnded := false
	for _, n := range segments {
		dataSize += int(n)
		if !packetEnded {
			firstPacket += int(n)
			packetEnded = n < 255
		}
	}
	pageSize := int64(27 + segmentCount + dataSize)
	if pageSize > size-off {
		return oggPage{}, false
	}
	end := off + pageSize
	if !packetEnded {
		firstPacket = 0
	}
	return oggPage{
		flags: header[5], granule: binary.LittleEndian.Uint64(header[6:14]),
		serial: binary.LittleEndian.Uint32(header[14:18]), end: end,
		data: off + int64(27+segmentCount), packet: firstPacket,
	}, true
}
