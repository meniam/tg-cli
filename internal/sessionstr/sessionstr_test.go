package sessionstr

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// audit07Key is a fixed key: a failure must reproduce from the log.
func audit07Key() []byte {
	key := make([]byte, 256)
	for i := range key {
		key[i] = byte(i % 251)
	}
	return key
}

func TestFormats(t *testing.T) {
	key := audit07Key()
	id := sha1.Sum(key)
	var s sessionFile
	s.Version = 1
	s.Data.DC = 2
	s.Data.UserID = 123
	s.Data.AuthKey = key
	s.Data.AuthKeyID = id[12:]
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	// The user id and the api id differ, so a swapped field is visible.
	const apiID = 456
	encoded, encodedData, err := Encode(raw, "desktop", apiID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Decode(encoded)
	if err != nil || d.UserID != 123 || d.DC != 2 || string(d.Session) != string(raw) {
		t.Fatalf("round trip: %+v %v", d, err)
	}
	if d.Client != "desktop" || d.APIID != apiID || d.Version != 1 || d.Format != "tgcli1" || d.KeyID != encodedKey(id) {
		t.Fatalf("round trip lost the metadata: %+v", d)
	}
	if d.CreatedAt.IsZero() || !d.CreatedAt.Equal(encodedData.CreatedAt) {
		t.Fatalf("created_at: decoded %v, encoded %v", d.CreatedAt, encodedData.CreatedAt)
	}
	if encodedData.Client != d.Client || encodedData.APIID != d.APIID || encodedData.UserID != d.UserID ||
		encodedData.DC != d.DC || encodedData.Version != d.Version || encodedData.Format != d.Format ||
		encodedData.KeyID != d.KeyID || string(encodedData.Session) != string(d.Session) {
		t.Fatalf("Encode returned %+v, Decode returned %+v", encodedData, d)
	}
	for _, ipLen := range []int{4, 16} {
		b := make([]byte, 1+ipLen+2+256)
		b[0] = 2
		binary.BigEndian.PutUint16(b[1+ipLen:], 443)
		copy(b[len(b)-256:], key)
		d, err = Decode("1" + base64.URLEncoding.EncodeToString(b))
		if err != nil || d.Format != "telethon" || d.KeyID != encodedKey(id) {
			t.Fatal("telethon decode failed", err)
		}
		audit07CheckForeignSession(t, d, 2, key)
	}
	for _, host := range []string{"127.0.0.1", "2001:db8::1", "example.org"} {
		b := make([]byte, 1+2+len(host)+2+256)
		b[0] = 2
		binary.BigEndian.PutUint16(b[1:], uint16(len(host)))
		copy(b[3:], host)
		binary.BigEndian.PutUint16(b[3+len(host):], 443)
		copy(b[len(b)-256:], key)
		d, err = Decode("1" + base64.StdEncoding.EncodeToString(b))
		if err != nil || d.Format != "gramjs" || d.KeyID != encodedKey(id) {
			t.Fatal("gramjs decode failed", err)
		}
		audit07CheckForeignSession(t, d, 2, key)
	}
	for _, bad := range []string{"secret", "1secret", "tgcli1.c2VjcmV0", strings.Repeat("x", MaxSize+1)} {
		_, err = Decode(bad)
		if err == nil || strings.Contains(err.Error(), bad) {
			t.Fatal("invalid input accepted or leaked")
		}
	}
}

// audit07CheckForeignSession checks the data of a session converted from a
// foreign format: the dc byte and the key reach the primary session file.
func audit07CheckForeignSession(t *testing.T, d Data, dc int, key []byte) {
	t.Helper()
	if d.DC != dc {
		t.Errorf("%s: dc = %d, want %d", d.Format, d.DC, dc)
	}
	gotDC, _, keyID, err := Inspect(d.Session)
	if err != nil || gotDC != dc || keyID != d.KeyID {
		t.Errorf("%s: the converted session is not valid for the key: dc=%d key=%s err=%v", d.Format, gotDC, keyID, err)
	}
	var parsed sessionFile
	if err := json.Unmarshal(d.Session, &parsed); err != nil || string(parsed.Data.AuthKey) != string(key) {
		t.Errorf("%s: the converted session lost the auth key: %v", d.Format, err)
	}
}

func encodedKey(id [20]byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 16)
	for i, b := range id[12:] {
		out[i*2] = digits[b>>4]
		out[i*2+1] = digits[b&15]
	}
	return string(out)
}

func TestRejectAmbiguousForeignFormat(t *testing.T) {
	for _, n := range []int{2, 14} {
		b := make([]byte, 1+2+n+2+256)
		b[0] = 2
		binary.BigEndian.PutUint16(b[1:3], uint16(n))
		copy(b[3:], strings.Repeat("a", n))
		binary.BigEndian.PutUint16(b[3+n:], 443)
		input := "1" + base64.URLEncoding.EncodeToString(b)
		_, err := Decode(input)
		if !errors.Is(err, ErrAmbiguous) || strings.Contains(err.Error(), input) {
			t.Fatal("ambiguous session was accepted or leaked")
		}
	}
}

// TestAudit07_ForeignInputBranches covers the inputs the happy paths miss: a
// zero dc byte, a declared host length that does not match the size, and a
// tgcli1 wrapper whose metadata disagrees with the session inside.
func TestAudit07_ForeignInputBranches(t *testing.T) {
	key := audit07Key()
	gramjs := func(dc byte, declared, host int) string {
		b := make([]byte, 1+2+host+2+256)
		b[0] = dc
		binary.BigEndian.PutUint16(b[1:], uint16(declared))
		copy(b[len(b)-256:], key)
		return "1" + base64.StdEncoding.EncodeToString(b)
	}
	telethon := func(dc byte) string {
		b := make([]byte, 1+4+2+256)
		b[0] = dc
		copy(b[len(b)-256:], key)
		return "1" + base64.URLEncoding.EncodeToString(b)
	}
	for name, input := range map[string]string{
		"telethon with a zero dc": telethon(0),
		"gramjs with a zero dc":   gramjs(0, 9, 9),
		"gramjs host longer":      gramjs(2, 10, 9),
		"gramjs host shorter":     gramjs(2, 8, 9),
		"gramjs empty host":       gramjs(2, 0, 9),
		"too short":               "1" + base64.StdEncoding.EncodeToString(make([]byte, 100)),
	} {
		if d, err := Decode(input); err == nil {
			t.Errorf("%s: accepted: %+v", name, d)
		}
	}
	if d, err := Decode(telethon(5)); err != nil || d.DC != 5 || d.Format != "telethon" {
		t.Errorf("telethon dc 5: %+v %v", d, err)
	}

	id := sha1.Sum(key)
	var s sessionFile
	s.Version = 1
	s.Data.DC = 2
	s.Data.UserID = 123
	s.Data.AuthKey = key
	s.Data.AuthKeyID = id[12:]
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	wrap := func(d Data) string {
		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		return "tgcli1." + base64.RawURLEncoding.EncodeToString(b)
	}
	good := Data{Version: 1, Session: raw, DC: 2, UserID: 123}
	if _, err := Decode(wrap(good)); err != nil {
		t.Fatalf("a consistent wrapper was refused: %v", err)
	}
	for name, d := range map[string]Data{
		"dc differs":      {Version: 1, Session: raw, DC: 3, UserID: 123},
		"user differs":    {Version: 1, Session: raw, DC: 2, UserID: 124},
		"version differs": {Version: 2, Session: raw, DC: 2, UserID: 123},
		"no session":      {Version: 1, DC: 2, UserID: 123},
	} {
		if _, err := Decode(wrap(d)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
