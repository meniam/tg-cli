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
	}{{"02-29", true}, {"2000-02-29", true}, {"2001-02-29", false}, {"1899-01-01", false}, {"2027-01-01", false}, {"13-01", false}} {
		_, err := ParseBirthday(tc.value, now)
		if (err == nil) != tc.valid {
			t.Errorf("birthday %q: %v", tc.value, err)
		}
	}
	if err := (AccountSetParams{}).Validate(now); err == nil {
		t.Fatal("empty settings accepted")
	}
	if err := (AccountSetParams{EmojiStatusUntil: "2027-01-01T00:00:00Z"}).Validate(now); err == nil {
		t.Fatal("until without status accepted")
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
