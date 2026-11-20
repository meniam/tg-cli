package rpc

import (
	"testing"
	"time"
)

func TestValidateAccountSettings(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		valid bool
		want  Birthday
	}{
		{"02-29", true, Birthday{Day: 29, Month: 2}},
		{"12-31", true, Birthday{Day: 31, Month: 12}},
		{"2000-02-29", true, Birthday{Day: 29, Month: 2, Year: 2000}},
		{"1900-01-05", true, Birthday{Day: 5, Month: 1, Year: 1900}},
		{"2026-03-04", true, Birthday{Day: 4, Month: 3, Year: 2026}},
		{"2001-02-29", false, Birthday{}},
		{"1899-01-01", false, Birthday{}},
		{"2027-01-01", false, Birthday{}},
		{"13-01", false, Birthday{}},
	} {
		got, err := ParseBirthday(tc.value, now)
		if (err == nil) != tc.valid {
			t.Errorf("birthday %q: %v", tc.value, err)
		}
		if got != tc.want {
			t.Errorf("birthday %q = %+v, want %+v", tc.value, got, tc.want)
		}
	}
	if err := (AccountSetParams{}).Validate(now); err == nil {
		t.Fatal("empty settings accepted")
	}
	if err := (AccountSetParams{EmojiStatusUntil: "2027-01-01T00:00:00Z"}).Validate(now); err == nil {
		t.Fatal("until without status accepted")
	}
	str := func(s string) *string { return &s }
	for name, p := range map[string]AccountSetParams{
		"leap day":        {Birthday: str("02-29")},
		"clear birthday":  {Birthday: str("none")},
		"channel":         {PersonalChannel: str("@news")},
		"status":          {EmojiStatus: str("5")},
		"clear status":    {EmojiStatus: str("none")},
		"status in time":  {EmojiStatus: str("5"), EmojiStatusUntil: "2027-01-01T00:00:00Z"},
		"several at once": {Birthday: str("2000-02-29"), PersonalChannel: str("@news"), EmojiStatus: str("7")},
	} {
		if err := p.Validate(now); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	for name, p := range map[string]AccountSetParams{
		"empty channel":   {PersonalChannel: str("")},
		"status not id":   {EmojiStatus: str("smile")},
		"status zero":     {EmojiStatus: str("0")},
		"until in past":   {EmojiStatus: str("5"), EmojiStatusUntil: "2025-01-01T00:00:00Z"},
		"until not RFC":   {EmojiStatus: str("5"), EmojiStatusUntil: "tomorrow"},
		"until too far":   {EmojiStatus: str("5"), EmojiStatusUntil: "2040-01-01T00:00:00Z"},
		"until for clear": {EmojiStatus: str("none"), EmojiStatusUntil: "2027-01-01T00:00:00Z"},
		"bad birthday":    {Birthday: str("13-01")},
	} {
		if p.Validate(now) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
func TestValidateChatSettings(t *testing.T) {
	if err := (ChatSetParams{}).Validate(); err == nil {
		t.Fatal("empty settings accepted")
	}
	invalid := "maybe"
	if err := (ChatSetParams{Forum: &invalid}).Validate(); err == nil {
		t.Fatal("invalid value accepted")
	}
	on, off, visible, hidden := "on", "off", "visible", "hidden"
	for name, p := range map[string]ChatSetParams{
		"forum on":       {Forum: &on},
		"antispam off":   {Antispam: &off},
		"prehistory":     {Prehistory: &visible},
		"prehistory off": {Prehistory: &hidden},
		"several":        {Signatures: &on, JoinRequests: &off, Prehistory: &hidden},
	} {
		if err := p.Validate(); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	for name, p := range map[string]ChatSetParams{
		"prehistory on":  {Prehistory: &on},
		"forum visible":  {Forum: &visible},
		"one bad of two": {Signatures: &on, JoinToSend: &invalid},
	} {
		if p.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPrivacyValidation(t *testing.T) {
	p := PrivacySetParams{Key: "last-seen", Default: "contacts"}
	if p.Validate() == nil {
		t.Fatal("unconfirmed set accepted")
	}
	p.DryRun = true
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.AllowBots = true
	p.DenyBots = true
	if p.Validate() == nil {
		t.Fatal("conflicting bot rules accepted")
	}
	if ValidPrivacyKey("unknown") {
		t.Fatal("unknown key accepted")
	}
}
