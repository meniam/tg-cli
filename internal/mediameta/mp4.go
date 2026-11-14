package mediameta

import (
	"encoding/binary"
	"io"
	"math"
)

const maxMP4Atoms = 4096
const maxMP4Depth = 8

type atom struct {
	typeName string
	end      int64
	data     int64
}

type mp4Scan struct {
	r          io.ReaderAt
	atoms      int
	timescale  uint32
	duration   uint64
	foundMovie bool
	foundVideo bool
	width      int
	height     int
}

type mp4Track struct {
	video  bool
	width  int
	height int
}

// Video reads the movie duration and the first video track's dimensions.
// Accepted ftyp brands: isom, iso2-iso6, mp41, mp42, avc1, M4V (space-padded).
// The list is deliberately narrower than all ISO BMFF brands.
func Video(r io.ReaderAt, size int64) (duration, width, height int, err error) {
	if size < 16 {
		return 0, 0, 0, ErrNotVideo
	}
	s := mp4Scan{r: r}
	first, err := s.readAtom(0, size)
	if err != nil || first.typeName != "ftyp" || !supportedVideoBrand(r, first) {
		return 0, 0, 0, ErrNotVideo
	}
	for off := first.end; off < size; {
		a, err := s.readAtom(off, size)
		if err != nil {
			return 0, 0, 0, ErrInvalidVideo
		}
		if a.typeName == "moov" {
			if s.foundMovie || s.walk(a.data, a.end, 1, true, nil) != nil {
				return 0, 0, 0, ErrInvalidVideo
			}
			s.foundMovie = true
		}
		off = a.end
	}
	if !s.foundMovie || s.timescale == 0 || s.duration == 0 || !s.foundVideo || s.width <= 0 || s.height <= 0 {
		return 0, 0, 0, ErrInvalidVideo
	}
	seconds := s.duration / uint64(s.timescale)
	if s.duration%uint64(s.timescale) != 0 {
		seconds++
	}
	if seconds == 0 || seconds > uint64(math.MaxInt) {
		return 0, 0, 0, ErrInvalidVideo
	}
	return int(seconds), s.width, s.height, nil
}

func (s *mp4Scan) readAtom(off, end int64) (atom, error) {
	s.atoms++
	if s.atoms > maxMP4Atoms || off < 0 || end-off < 8 {
		return atom{}, ErrInvalidVideo
	}
	var header [16]byte
	if !readExact(s.r, off, header[:8]) {
		return atom{}, ErrInvalidVideo
	}
	sz := uint64(binary.BigEndian.Uint32(header[:4]))
	headerLen := int64(8)
	if sz == 1 {
		if end-off < 16 || !readExact(s.r, off+8, header[8:]) {
			return atom{}, ErrInvalidVideo
		}
		sz = binary.BigEndian.Uint64(header[8:])
		headerLen = 16
	} else if sz == 0 {
		sz = uint64(end - off)
	}
	if sz < uint64(headerLen) || sz > uint64(end-off) {
		return atom{}, ErrInvalidVideo
	}
	return atom{typeName: string(header[4:8]), end: off + int64(sz), data: off + headerLen}, nil
}

func (s *mp4Scan) walk(start, end int64, depth int, inMovie bool, track *mp4Track) error {
	if depth > maxMP4Depth {
		return ErrInvalidVideo
	}
	for off := start; off < end; {
		a, err := s.readAtom(off, end)
		if err != nil {
			return err
		}
		switch a.typeName {
		case "moov":
			if err := s.walk(a.data, a.end, depth+1, true, nil); err != nil {
				return err
			}
		case "trak":
			if !inMovie {
				break
			}
			t := &mp4Track{}
			if err := s.walk(a.data, a.end, depth+1, true, t); err != nil {
				return err
			}
			if t.video && !s.foundVideo {
				s.foundVideo, s.width, s.height = true, t.width, t.height
			}
		case "mdia":
			if track != nil {
				if err := s.walk(a.data, a.end, depth+1, inMovie, track); err != nil {
					return err
				}
			}
		case "mvhd":
			if inMovie && track == nil {
				if s.timescale != 0 || s.readMVHD(a) != nil {
					return ErrInvalidVideo
				}
			}
		case "tkhd":
			if track != nil && s.readTKHD(a, track) != nil {
				return ErrInvalidVideo
			}
		case "hdlr":
			if track != nil {
				var data [12]byte
				if a.end-a.data < int64(len(data)) || !readExact(s.r, a.data, data[:]) {
					return ErrInvalidVideo
				}
				track.video = string(data[8:12]) == "vide"
			}
		}
		off = a.end
	}
	return nil
}

func (s *mp4Scan) readMVHD(a atom) error {
	var data [32]byte
	if a.end-a.data < 20 || !readExact(s.r, a.data, data[:20]) {
		return ErrInvalidVideo
	}
	switch data[0] {
	case 0:
		s.timescale = binary.BigEndian.Uint32(data[12:16])
		s.duration = uint64(binary.BigEndian.Uint32(data[16:20]))
	case 1:
		if a.end-a.data < 32 || !readExact(s.r, a.data, data[:]) {
			return ErrInvalidVideo
		}
		s.timescale = binary.BigEndian.Uint32(data[20:24])
		s.duration = binary.BigEndian.Uint64(data[24:32])
	default:
		return ErrInvalidVideo
	}
	if s.timescale == 0 || s.duration == 0 {
		return ErrInvalidVideo
	}
	return nil
}

func (s *mp4Scan) readTKHD(a atom, track *mp4Track) error {
	var data [96]byte
	if a.end-a.data < 84 || !readExact(s.r, a.data, data[:84]) {
		return ErrInvalidVideo
	}
	offset := 76
	switch data[0] {
	case 0:
	case 1:
		if a.end-a.data < 96 || !readExact(s.r, a.data, data[:]) {
			return ErrInvalidVideo
		}
		offset = 88
	default:
		return ErrInvalidVideo
	}
	track.width = int(binary.BigEndian.Uint32(data[offset:offset+4]) >> 16)
	track.height = int(binary.BigEndian.Uint32(data[offset+4:offset+8]) >> 16)
	return nil
}

func supportedVideoBrand(r io.ReaderAt, a atom) bool {
	if a.end-a.data < 8 || (a.end-a.data)%4 != 0 || a.end-a.data > 4096 {
		return false
	}
	var brand [4]byte
	for off := a.data; off < a.end; off += 4 {
		if off == a.data+4 { // minor_version, not a brand
			continue
		}
		if !readExact(r, off, brand[:]) {
			return false
		}
		switch string(brand[:]) {
		case "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "M4V ":
			return true
		}
	}
	return false
}

func readExact(r io.ReaderAt, off int64, data []byte) bool {
	n, err := r.ReadAt(data, off)
	return n == len(data) && err == nil
}
