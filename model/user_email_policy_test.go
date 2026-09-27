package model

import "testing"

func TestIsRegistrationEmailAllowed(t *testing.T) {
	for _, test := range []struct {
		email string
		want  bool
	}{
		{email: "user@gmail.com", want: true},
		{email: "user.name@gmail.com", want: false},
		{email: "user+tag@gmail.com", want: false},
		{email: "user@qq.com", want: true},
	} {
		if got := IsRegistrationEmailAllowed(test.email); got != test.want {
			t.Errorf("IsRegistrationEmailAllowed(%q) = %v, want %v", test.email, got, test.want)
		}
	}
}
