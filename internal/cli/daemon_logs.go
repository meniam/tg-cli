package cli

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"tg-cli/internal/exitcode"
	"tg-cli/internal/output"
)

const maxLogLines = 10000

type daemonLogsResult struct {
	Lines []string `json:"lines"`
}

func (a *app) daemonLogs(cmd *cobra.Command, _ []string) error {
	tail, _ := cmd.Flags().GetInt("tail")
	if (cmd.Flags().Changed("tail") && tail < 1) || tail > maxLogLines {
		return fail(exitcode.Usage, "--tail must be between 1 and %d", maxLogLines)
	}
	if tail == 0 {
		tail = maxLogLines
	}
	_, paths, err := a.profile(cmd)
	if err != nil {
		return err
	}
	f, err := os.Open(paths.Log)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fail(exitcode.NotFound, "daemon journal does not exist: %s", paths.Log)
		}
		return fail(exitcode.Failure, "open daemon journal: %v", err)
	}
	defer f.Close()
	settings, err := a.outputSettingsFor(cmd)
	if err != nil {
		return err
	}
	lines, err := lastLogLines(f, tail)
	if err != nil {
		return fail(exitcode.Failure, "read daemon journal: %v", err)
	}
	if err := a.emitLogLines(settings, lines); err != nil {
		return fail(exitcode.Failure, "print daemon journal: %v", err)
	}
	follow, _ := cmd.Flags().GetBool("follow")
	if !follow {
		return nil
	}
	info, err := f.Stat()
	if err != nil {
		return fail(exitcode.Failure, "stat daemon journal: %v", err)
	}
	return a.followLog(cmd.Context(), settings, paths.Log, f, info.Size())
}

func (a *app) emitLogLines(settings outputSettings, lines []string) error {
	if settings.format == output.FormatJSONL {
		return output.Lines(a.deps.Stdout, lines, "")
	}
	return a.emit(settings, daemonLogsResult{Lines: lines}, nil)
}

func lastLogLines(f *os.File, n int) ([]string, error) {
	lines := make([]string, 0, n)
	next := 0
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for scanner.Scan() {
		if len(lines) == n {
			lines[next] = scanner.Text()
			next = (next + 1) % n
		} else {
			lines = append(lines, scanner.Text())
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if next == 0 {
		return lines, nil
	}
	return append(lines[next:], lines[:next]...), nil
}

// followLog holds the current descriptor until a replacement journal appears.
// It only emits complete lines, so a concurrent append cannot split an entry.
func (a *app) followLog(ctx context.Context, settings outputSettings, path string, current *os.File, offset int64) error {
	defer func() { _ = current.Close() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		if _, err := current.Seek(offset, io.SeekStart); err != nil {
			return fail(exitcode.Failure, "seek daemon journal: %v", err)
		}
		reader := bufio.NewReader(current)
		var lines []string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if !errors.Is(err, io.EOF) {
					return fail(exitcode.Failure, "read daemon journal: %v", err)
				}
				break
			}
			offset += int64(len(line))
			lines = append(lines, strings.TrimSuffix(line, "\n"))
		}
		if len(lines) > 0 {
			if err := a.emitLogLines(settings, lines); err != nil {
				return fail(exitcode.Failure, "print daemon journal: %v", err)
			}
		}
		pathInfo, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fail(exitcode.Failure, "stat daemon journal: %v", err)
		}
		currentInfo, err := current.Stat()
		if err != nil {
			return fail(exitcode.Failure, "stat open daemon journal: %v", err)
		}
		if os.SameFile(pathInfo, currentInfo) && pathInfo.Size() >= offset {
			continue
		}
		newFile, err := os.Open(path)
		if err != nil {
			return fail(exitcode.Failure, "open rotated daemon journal: %v", err)
		}
		if err := current.Close(); err != nil {
			_ = newFile.Close()
			return fail(exitcode.Failure, "close old daemon journal: %v", err)
		}
		current, offset = newFile, 0
	}
}
