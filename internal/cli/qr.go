package cli

import (
	"fmt"
	"io"
	"strings"

	"rsc.io/qr"
)

// quietZone is the white border around a QR code, in modules. Scanners need it.
const quietZone = 2

// renderQR draws text as a QR code of terminal characters, two QR rows per
// text line. With color the modules use explicit black and white backgrounds,
// so the code stays scannable on light and dark themes; without it plain
// block characters are used, which only suits a dark terminal.
func renderQR(w io.Writer, text string, color bool) error {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return fmt.Errorf("encode the QR code: %w", err)
	}
	black := func(x, y int) bool {
		if x < 0 || y < 0 || x >= code.Size || y >= code.Size {
			return false // the quiet zone is white
		}
		return code.Black(x, y)
	}

	var b strings.Builder
	for y := -quietZone; y < code.Size+quietZone; y += 2 {
		for x := -quietZone; x < code.Size+quietZone; x++ {
			top, bottom := black(x, y), black(x, y+1)
			if color {
				// Upper half block: foreground is the top module, background the bottom one.
				fmt.Fprintf(&b, "\x1b[%d;%dm▀", fg(top), bg(bottom))
				continue
			}
			switch {
			case top && bottom:
				b.WriteString("█") // full block
			case top:
				b.WriteString("▀") // upper half
			case bottom:
				b.WriteString("▄") // lower half
			default:
				b.WriteString(" ")
			}
		}
		if color {
			b.WriteString("\x1b[0m")
		}
		b.WriteString("\n")
	}
	_, err = io.WriteString(w, b.String())
	return err
}

func fg(black bool) int {
	if black {
		return 30
	}
	return 97
}

func bg(black bool) int {
	if black {
		return 40
	}
	return 107
}
