package sessionstr

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestFormats(t *testing.T) {
	key := make([]byte, 256)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	id := sha1.Sum(key)
	var s sessionFile
	s.Version = 1
	s.Data.DC = 2
	s.Data.UserID = 123
	s.Data.AuthKey = key
	s.Data.AuthKeyID = id[12:]
	raw, _ := json.Marshal(s)
	encoded, _, err := Encode(raw, "desktop", 123)
	if err != nil {
		t.Fatal(err)
	}
	d, err := Decode(encoded)
	if err != nil || d.UserID != 123 || d.DC != 2 || string(d.Session) != string(raw) {
		t.Fatalf("round trip: %+v %v", d, err)
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
	}
	for _, bad := range []string{"secret", "1secret", "tgcli1.c2VjcmV0", strings.Repeat("x", MaxSize+1)} {
		_, err = Decode(bad)
		if err == nil || strings.Contains(err.Error(), bad) {
			t.Fatal("invalid input accepted or leaked")
		}
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
		if err == nil || !strings.Contains(err.Error(), "ambiguous") || strings.Contains(err.Error(), input) {
			t.Fatal("ambiguous session was accepted or leaked")
		}
	}
}
