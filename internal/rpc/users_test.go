package rpc

import "testing"

func TestUserParamsValidation(t *testing.T) {
	for _, tc := range []struct {
		value        string
		phone, valid bool
	}{{"@valid_name", false, true}, {"42", false, true}, {"-42", true, false}, {"+123456789", false, false}, {"+123456789", true, true}, {"t.me/+abcdefgh", true, false}, {"", true, false}} {
		if (ValidateUserRef(tc.value, tc.phone) == nil) != tc.valid {
			t.Errorf("reference %q", tc.value)
		}
	}
	for _, p := range []ContactListParams{{Limit: -1}, {Limit: 1001}, {Offset: -1}} {
		if ValidateContactListParams(p) == nil {
			t.Errorf("accepted %+v", p)
		}
	}
	for _, p := range []ContactSearchParams{{Query: " "}, {Query: "a", Limit: -1}, {Query: "a", Limit: 101}} {
		if ValidateContactSearchParams(p) == nil {
			t.Errorf("accepted %+v", p)
		}
	}
}
