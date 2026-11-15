package cli

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"tg-cli/internal/exitcode"
)

func secretFile(path string, limit int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fail(exitcode.Usage, "cannot open secret file")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return "", fail(exitcode.Usage, "secret file must be a regular owner-only file")
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok || sys.Uid != uint32(os.Geteuid()) {
		return "", fail(exitcode.Usage, "secret file must belong to the current user")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return "", fail(exitcode.Usage, "cannot read secret file or file is too large")
	}
	s := trimLine(string(b))
	if s == "" {
		return "", fail(exitcode.Usage, "secret must not be empty")
	}
	return s, nil
}
func trimLine(s string) string {
	if strings.HasSuffix(s, "\n") {
		s = strings.TrimSuffix(s, "\n")
		s = strings.TrimSuffix(s, "\r")
	}
	return s
}
func secretLine(r io.Reader, max int) (string, error) {
	b, err := bufio.NewReader(io.LimitReader(r, int64(max)+2)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fail(exitcode.Usage, "cannot read input")
	}
	s := trimLine(b)
	if len(s) == 0 || len(s) > max {
		return "", fail(exitcode.Usage, "input is empty or too long")
	}
	return s, nil
}
