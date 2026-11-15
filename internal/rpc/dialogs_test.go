package rpc

import "testing"

func TestTerminateParamsValidation(t *testing.T) {
	for _, tc := range []struct {
		p     TerminateParams
		valid bool
	}{
		{TerminateParams{ID: "0123456789abcdef", Yes: true}, true},
		{TerminateParams{ID: "0123456789abcdef", DryRun: true}, true},
		{TerminateParams{ID: "0123456789abcdef"}, false},
		{TerminateParams{ID: "0123456789abcdeF", Yes: true}, false},
		{TerminateParams{ID: "123", Yes: true}, false},
	} {
		if err := ValidateTerminateParams(tc.p); (err == nil) != tc.valid {
			t.Errorf("%+v: %v", tc.p, err)
		}
	}
}
