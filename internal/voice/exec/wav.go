package exec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrBadWAV means the file is not a 16 kHz mono PCM16 WAV.
var ErrBadWAV = errors.New("bad wav")

const (
	wavRate     = 16000
	wavChannels = 1
	wavBits     = 16
	// bytesPerSecond of 16 kHz mono PCM16.
	bytesPerSecond = wavRate * wavChannels * wavBits / 8
	// maxWAVChunks bounds the header walk on a damaged file.
	maxWAVChunks = 64
)

// WavSeconds returns the duration of a 16 kHz mono PCM16 WAV from its header
// and the data size, without running any program. ffmpeg puts a LIST chunk
// before the data, so the chunks are walked rather than assumed at fixed offsets.
// A data size the header cannot be trusted on (zero, 0xFFFFFFFF or past the end of
// the file, as in streamed output) falls back to the bytes actually present.
func WavSeconds(path string) (float64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}

	var head [12]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return 0, fmt.Errorf("%w: short header", ErrBadWAV)
	}
	if string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return 0, fmt.Errorf("%w: not a RIFF/WAVE file", ErrBadWAV)
	}

	offset := int64(len(head))
	haveFmt := false
	for range maxWAVChunks {
		var ch [8]byte
		if _, err := io.ReadFull(f, ch[:]); err != nil {
			return 0, fmt.Errorf("%w: no data chunk", ErrBadWAV)
		}
		id := string(ch[0:4])
		size := int64(binary.LittleEndian.Uint32(ch[4:8]))
		offset += 8

		switch id {
		case "fmt ":
			if size < 16 {
				return 0, fmt.Errorf("%w: fmt chunk is too short", ErrBadWAV)
			}
			var ft [16]byte
			if _, err := io.ReadFull(f, ft[:]); err != nil {
				return 0, fmt.Errorf("%w: short fmt chunk", ErrBadWAV)
			}
			format := binary.LittleEndian.Uint16(ft[0:2])
			channels := binary.LittleEndian.Uint16(ft[2:4])
			rate := binary.LittleEndian.Uint32(ft[4:8])
			bits := binary.LittleEndian.Uint16(ft[14:16])
			if format != 1 || channels != wavChannels || rate != wavRate || bits != wavBits {
				return 0, fmt.Errorf("%w: want PCM16 16000 Hz mono, got format %d, %d channels, %d Hz, %d bits",
					ErrBadWAV, format, channels, rate, bits)
			}
			haveFmt = true
			skip := size - 16 + size%2
			if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
				return 0, err
			}
			offset += size + size%2
		case "data":
			if !haveFmt {
				return 0, fmt.Errorf("%w: data chunk before fmt chunk", ErrBadWAV)
			}
			present := info.Size() - offset
			if size == 0 || size == 0xFFFFFFFF || size > present {
				size = present
			}
			if size <= 0 {
				return 0, fmt.Errorf("%w: no audio data", ErrBadWAV)
			}
			// Whole samples only.
			size -= size % (wavBits / 8 * wavChannels)
			return float64(size) / bytesPerSecond, nil
		default:
			skip := size + size%2
			if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
				return 0, err
			}
			offset += skip
		}
	}
	return 0, fmt.Errorf("%w: no data chunk in the first %d chunks", ErrBadWAV, maxWAVChunks)
}
