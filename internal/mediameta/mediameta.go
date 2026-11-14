// Package mediameta checks media signatures and reads bounded metadata from files.
package mediameta

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
)

var (
	ErrNotPhoto     = errors.New("file is not a JPEG or PNG photo")
	ErrNotVideo     = errors.New("file is not a supported MP4 video")
	ErrInvalidVideo = errors.New("invalid MP4 video metadata")
	ErrNotOpus      = errors.New("file is not Ogg Opus audio")
	ErrInvalidOpus  = errors.New("invalid Ogg Opus metadata")
	ErrUnknown      = errors.New("unknown audio format")
)

// Image reads JPEG or PNG dimensions without decoding the whole image.
func Image(r io.ReaderAt, size int64) (width, height int, err error) {
	if size < 3 {
		return 0, 0, ErrNotPhoto
	}
	var head [8]byte
	n, _ := r.ReadAt(head[:], 0)
	if !bytes.HasPrefix(head[:n], []byte{0xff, 0xd8, 0xff}) &&
		!bytes.Equal(head[:n], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) {
		return 0, 0, ErrNotPhoto
	}
	config, format, err := image.DecodeConfig(io.NewSectionReader(r, 0, size))
	if err != nil || (format != "jpeg" && format != "png") || config.Width <= 0 || config.Height <= 0 {
		return 0, 0, ErrNotPhoto
	}
	return config.Width, config.Height, nil
}
