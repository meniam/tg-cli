package chatref

import "testing"

func TestParseValid(t *testing.T) {
	tests := []struct {
		in   string
		want Ref
	}{
		{"@telegram", Ref{Kind: Username, Username: "telegram"}},
		{"Telegram", Ref{Kind: Username, Username: "telegram"}},
		{"  @Durov_Official  ", Ref{Kind: Username, Username: "durov_official"}},
		{"t.me/telegram", Ref{Kind: Username, Username: "telegram"}},
		{"https://t.me/telegram", Ref{Kind: Username, Username: "telegram"}},
		{"https://t.me/telegram/", Ref{Kind: Username, Username: "telegram"}},
		{"https://t.me/telegram/123", Ref{Kind: Username, Username: "telegram"}},
		{"https://telegram.me/telegram", Ref{Kind: Username, Username: "telegram"}},
		{"tg://resolve?domain=telegram", Ref{Kind: Username, Username: "telegram"}},
		{"tg:resolve?domain=telegram", Ref{Kind: Username, Username: "telegram"}},
		{"12345", Ref{Kind: ID, ID: 12345}},
		{"-12345", Ref{Kind: ID, ID: -12345}},
		{"-1001234567890", Ref{Kind: ID, ID: -1001234567890}},
		{"+15551234567", Ref{Kind: Phone, Phone: "15551234567"}},
		{"+1 (555) 123-4567", Ref{Kind: Phone, Phone: "15551234567"}},
		{"https://t.me/+AbCdEfGh1234", Ref{Kind: Invite, Invite: "AbCdEfGh1234"}},
		{"t.me/+AbCdEfGh1234", Ref{Kind: Invite, Invite: "AbCdEfGh1234"}},
		{"https://t.me/joinchat/AbCdEfGh1234", Ref{Kind: Invite, Invite: "AbCdEfGh1234"}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	for _, in := range []string{
		"", "   ", "@", "@a", "@abcd", "@1abcde", "@bad name", "@bad-name", "name with spaces",
		"+", "+12", "+1234567890123456", "+12abc345678", "0", "-0", "-1000000000000", "12a",
		"tg://join?invite=abc", "tg://resolve", "t.me/+short", "https://t.me/joinchat/", "javascript:alert(1)",
		"t.me/addlist/AbCdEfGh", "https://t.me/ADDLIST/AbCdEfGh", "t.me/addlist/",
	} {
		t.Run(in, func(t *testing.T) {
			if got, err := Parse(in); err == nil {
				t.Errorf("%q must be rejected, got %+v", in, got)
			}
		})
	}
}

func TestIDKinds(t *testing.T) {
	if !IsChannel(-1001234567890) || IsChannel(-12345) || IsChannel(12345) {
		t.Error("IsChannel is wrong")
	}
	if !IsGroup(-12345) || IsGroup(-1001234567890) || IsGroup(12345) {
		t.Error("IsGroup is wrong")
	}
	if id, ok := ChannelID(-1001234567890); !ok || id != 1234567890 {
		t.Errorf("ChannelID = %d, %v", id, ok)
	}
	if _, ok := ChannelID(-12345); ok {
		t.Error("a basic group is not a channel")
	}
	if id, ok := GroupID(-12345); !ok || id != 12345 {
		t.Errorf("GroupID = %d, %v", id, ok)
	}
	if _, ok := GroupID(12345); ok {
		t.Error("a user is not a group")
	}
}

func TestStringHidesSecrets(t *testing.T) {
	phone, _ := Parse("+15551234567")
	invite, _ := Parse("t.me/+AbCdEfGh1234")
	for _, r := range []Ref{phone, invite} {
		if s := r.String(); s == "" || containsAny(s, "15551234567", "AbCdEfGh1234") {
			t.Errorf("String() must not repeat the secret: %q", s)
		}
	}
	if (Ref{Kind: Username, Username: "telegram"}).String() != "@telegram" {
		t.Error("a username is printed with @")
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		for i := 0; i+len(p) <= len(s); i++ {
			if s[i:i+len(p)] == p {
				return true
			}
		}
	}
	return false
}
