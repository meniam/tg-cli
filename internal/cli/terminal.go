package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"tg-cli/internal/clientpreset"
	"tg-cli/internal/daemon"
)

// presetFetchTimeout bounds the download of the client presets.
const presetFetchTimeout = 15 * time.Second

// DefaultDeps returns the dependencies of the real process: the standard
// streams, the environment and the terminal. newTelegram creates the Telegram
// client of the daemon; main supplies it so that this package stays free of
// the Telegram library.
func DefaultDeps(newTelegram func(daemon.TelegramConfig) (daemon.Telegram, error)) Deps {
	stdin := bufio.NewReader(os.Stdin)
	return Deps{
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Getenv:      os.Getenv,
		NewTelegram: newTelegram,
		FetchPresets: func(ctx context.Context) ([]byte, error) {
			return clientpreset.Fetch(ctx, &http.Client{Timeout: presetFetchTimeout})
		},
		ReadLine: func(prompt string) (string, error) {
			fmt.Fprint(os.Stderr, prompt)
			line, err := stdin.ReadString('\n')
			if err != nil && (!errors.Is(err, io.EOF) || line == "") {
				return "", err
			}
			return strings.TrimSpace(line), nil
		},
		ReadSecret: func(prompt string) (string, error) {
			fd := int(os.Stdin.Fd())
			if !term.IsTerminal(fd) {
				return "", errors.New("no terminal: a secret cannot be read without echo, run the command in a terminal")
			}
			fmt.Fprint(os.Stderr, prompt)
			secret, err := term.ReadPassword(fd)
			fmt.Fprintln(os.Stderr)
			return string(secret), err
		},
	}
}
