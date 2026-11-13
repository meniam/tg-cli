// Package clientpreset resolves the application credentials (api_id and
// api_hash) of the official Telegram clients, for users who have no
// application of their own.
//
// The values are not stored in this repository. They are downloaded from a
// pinned commit of the opentele project, the same source the installed tg
// uses, and checked against a SHA-256 before use.
package clientpreset

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	// pinnedCommit is the opentele commit the credentials are read from. A
	// commit URL never changes its content.
	pinnedCommit = "1a6f0816eac47ff3cb907af72ed9f8cbbbe8fba0"
	// SourceURL is the file with the credentials.
	SourceURL = "https://raw.githubusercontent.com/thedemons/opentele/" + pinnedCommit + "/src/api.py"
	// sourceSHA256 is the checksum of that file.
	sourceSHA256 = "f2603f8382cffadb9b708237224fda75feb95b456a4ef408daff5503559911f9"
	// maxSourceSize bounds the download; the real file is about 25 KB.
	maxSourceSize = 1 << 20
)

// testAppID is a well-known test application that must never be used.
const testAppID = 17349

// Credentials identify an application to Telegram. AppHash is not secret for
// the official clients, but it is still never printed or logged.
type Credentials struct {
	AppID   int
	AppHash string
}

// names maps opentele class names to the preset names users type.
var names = map[string]string{
	"TelegramDesktop":  "desktop",
	"TelegramAndroid":  "android",
	"TelegramAndroidX": "android-x",
	"TelegramIOS":      "ios",
	"TelegramMacOS":    "macos",
	"TelegramWeb_Z":    "web-z",
	"TelegramWeb_K":    "web-k",
	"Webogram":         "webogram",
}

var (
	classLine = regexp.MustCompile(`^\s*class\s+(\w+)\(APIData\)\s*:`)
	idLine    = regexp.MustCompile(`^\s+api_id\s*=\s*(\d+)\s*$`)
	hashLine  = regexp.MustCompile(`^\s+api_hash\s*=\s*"([a-f0-9]{32})"\s*$`)
)

// Parse extracts the presets from the opentele api.py. Only live assignments
// count: commented-out lines are ignored.
func Parse(content []byte) (map[string]Credentials, error) {
	presets := map[string]Credentials{}
	var class string
	var cur Credentials
	flush := func() {
		if class == "" || cur.AppID == 0 || cur.AppHash == "" {
			return
		}
		if cur.AppID == testAppID || cur.AppID > 1_000_000 {
			return
		}
		name, ok := names[class]
		if !ok {
			name = strings.ToLower(class)
		}
		presets[name] = cur
	}

	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		if m := classLine.FindStringSubmatch(line); m != nil {
			flush()
			class, cur = m[1], Credentials{}
			continue
		}
		if class == "" {
			continue
		}
		// The last assignment wins: a docstring may quote older values above it.
		if m := idLine.FindStringSubmatch(line); m != nil {
			cur.AppID, _ = strconv.Atoi(m[1])
		} else if m := hashLine.FindStringSubmatch(line); m != nil {
			cur.AppHash = m[1]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	flush()
	if len(presets) == 0 {
		return nil, errors.New("no client presets found in the downloaded file")
	}
	return presets, nil
}

// Resolve finds a preset by name. The error of an unknown name lists the
// available names.
func Resolve(content []byte, name string) (Credentials, error) {
	presets, err := Parse(content)
	if err != nil {
		return Credentials{}, err
	}
	if c, ok := presets[name]; ok {
		return c, nil
	}
	available := make([]string, 0, len(presets))
	for n := range presets {
		available = append(available, n)
	}
	sort.Strings(available)
	return Credentials{}, fmt.Errorf("unknown client preset %q, available: %s", name, strings.Join(available, ", "))
}

// Fetch downloads the source file and checks its SHA-256.
func Fetch(ctx context.Context, hc *http.Client) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, SourceURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download the client presets: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSourceSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceSize {
		return nil, errors.New("download the client presets: the file is too large")
	}
	if err := verify(data); err != nil {
		return nil, err
	}
	return data, nil
}

func verify(data []byte) error {
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != sourceSHA256 {
		return errors.New("the downloaded client presets do not match the pinned checksum")
	}
	return nil
}
