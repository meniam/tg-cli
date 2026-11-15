// Package sessionstr encodes portable Telegram session credentials.
package sessionstr

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const MaxSize = 64 << 10

var errInvalid = errors.New("invalid session string")

// ErrAmbiguous means both supported foreign formats match the input.
var ErrAmbiguous = errors.New("ambiguous foreign session format")

type Data struct {
	Session   json.RawMessage `json:"session"`
	UserID    int64           `json:"user_id,omitempty"`
	DC        int             `json:"dc"`
	Client    string          `json:"client,omitempty"`
	APIID     int             `json:"api_id,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	Version   int             `json:"v"`
	Format    string          `json:"-"`
	KeyID     string          `json:"-"`
}

type sessionFile struct {
	Version int
	Data    struct {
		DC        int
		Addr      string
		AuthKey   []byte
		AuthKeyID []byte
		UserID    int64 `json:",omitempty"`
	}
}

// Inspect validates the primary key and extracts non-secret metadata.
func Inspect(raw []byte) (dc int, userID int64, keyID string, err error) {
	var s sessionFile
	if len(raw) > MaxSize || json.Unmarshal(raw, &s) != nil || s.Version != 1 || s.Data.DC < 1 || s.Data.DC > 255 || len(s.Data.AuthKey) != 256 || len(s.Data.AuthKeyID) != 8 {
		return 0, 0, "", errInvalid
	}
	id := sha1.Sum(s.Data.AuthKey)
	if string(id[12:]) != string(s.Data.AuthKeyID) {
		return 0, 0, "", errInvalid
	}
	return s.Data.DC, s.Data.UserID, hex.EncodeToString(id[12:]), nil
}

func Encode(raw []byte, client string, apiID int) (string, Data, error) {
	dc, uid, id, err := Inspect(raw)
	if err != nil {
		return "", Data{}, err
	}
	d := Data{Version: 1, Session: raw, DC: dc, UserID: uid, Client: client, APIID: apiID, CreatedAt: time.Now().UTC(), Format: "tgcli1", KeyID: id}
	b, err := json.Marshal(d)
	if err != nil || len(b) > MaxSize {
		return "", Data{}, errInvalid
	}
	out := "tgcli1." + base64.RawURLEncoding.EncodeToString(b)
	if len(out) > MaxSize {
		return "", Data{}, errInvalid
	}
	return out, d, nil
}

func Decode(input string) (Data, error) {
	var d Data
	input = strings.TrimSpace(input)
	if len(input) == 0 || len(input) > MaxSize {
		return d, errInvalid
	}
	if strings.HasPrefix(input, "tgcli1.") {
		b, e := base64.RawURLEncoding.DecodeString(input[7:])
		if e != nil || json.Unmarshal(b, &d) != nil || d.Version != 1 {
			return Data{}, errInvalid
		}
		dc, uid, id, e := Inspect(d.Session)
		if e != nil || dc != d.DC || uid != d.UserID {
			return Data{}, errInvalid
		}
		d.Format = "tgcli1"
		d.KeyID = id
		return d, nil
	}
	if input[0] != '1' {
		return d, errInvalid
	}
	var b []byte
	var e error
	for _, enc := range []*base64.Encoding{base64.URLEncoding, base64.RawURLEncoding, base64.StdEncoding, base64.RawStdEncoding} {
		b, e = enc.DecodeString(input[1:])
		if e == nil {
			break
		}
	}
	if e != nil {
		return d, errInvalid
	}
	var key []byte
	switch len(b) {
	case 263, 275:
		// These lengths can also encode a gramjs address of 2 or 14 bytes.
		// The formats share the version byte; never guess when both match.
		if int(binary.BigEndian.Uint16(b[1:3])) == len(b)-261 {
			return Data{}, ErrAmbiguous
		}
		key = b[len(b)-256:]
		d.Format = "telethon"
	default:
		if len(b) < 261 {
			return d, errInvalid
		}
		n := int(binary.BigEndian.Uint16(b[1:3]))
		if n == 0 || n > 32767 || len(b) != 1+2+n+2+256 {
			return d, errInvalid
		}
		key = b[len(b)-256:]
		d.Format = "gramjs"
	}
	if b[0] == 0 {
		return Data{}, errInvalid
	}
	id := sha1.Sum(key)
	var s sessionFile
	s.Version = 1
	s.Data.DC = int(b[0])
	s.Data.AuthKey = key
	s.Data.AuthKeyID = id[12:]
	raw, e := json.Marshal(s)
	if e != nil {
		return Data{}, errInvalid
	}
	d.Session = raw
	d.DC = int(b[0])
	d.KeyID = hex.EncodeToString(id[12:])
	return d, nil
}
