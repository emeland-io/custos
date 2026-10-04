package attest

import "testing"

func TestCanonical(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`{"b":1,"a":2}`, `{"a":2,"b":1}`},
		{" {\n  \"z\": [ 3, {\"y\":true,\"x\":null} ],\n  \"a\": \"s\"\n}\n", `{"a":"s","z":[3,{"x":null,"y":true}]}`},
		{`{"n":1.50,"m":1e2,"big":123456789012345678901234567890}`, `{"big":123456789012345678901234567890,"m":1e2,"n":1.50}`},
		{`{"html":"<a&b>","esc":"é\n"}`, `{"esc":"é\n","html":"<a&b>"}`},
		{`"text"`, `"text"`},
		{`[]`, `[]`},
		{`{"k":1,"k":2}`, `{"k":2}`},
	} {
		got, err := Canonical([]byte(tc.in))
		if err != nil {
			t.Errorf("Canonical(%s): %v", tc.in, err)
			continue
		}
		if string(got) != tc.want {
			t.Errorf("Canonical(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestCanonicalRejectsInvalidJSON(t *testing.T) {
	for _, in := range []string{``, `not json`, `{"a":1} {"b":2}`, `{"a":1`, `{"a":1}x`} {
		if got, err := Canonical([]byte(in)); err == nil {
			t.Errorf("Canonical(%q) = %s, want an error", in, got)
		}
	}
}
