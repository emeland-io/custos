package gitrepo

import "testing"

func TestParseSignature(t *testing.T) {
	sig, err := ParseSignature("  Jane Doe <jane@example.org> ")
	if err != nil || sig != (Signature{Name: "Jane Doe", Email: "jane@example.org"}) {
		t.Fatalf("%+v %v", sig, err)
	}
	if sig.String() != "Jane Doe <jane@example.org>" {
		t.Errorf("String() = %q", sig.String())
	}
	for _, bad := range []string{
		"",
		"Jane Doe",
		"jane@example.org",
		"<jane@example.org>",
		"Jane Doe <>",
		"Jane <x> <jane@example.org>",
		"Jane\nDoe <jane@example.org>",
		"Jane Doe <jane@example.org>\nX",
		"Jane Doe <jane @example.org>",
		"Jane Doe <jane@example.org",
	} {
		if _, err := ParseSignature(bad); err == nil {
			t.Errorf("ParseSignature(%q) accepted", bad)
		}
	}
}

func TestBot(t *testing.T) {
	if Bot.String() != "custos-bot <custos-bot@localhost>" {
		t.Errorf("Bot = %q", Bot.String())
	}
}
