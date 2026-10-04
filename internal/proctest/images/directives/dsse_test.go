package directives

import "testing"

// TestPAESpecVector checks PAE against the DSSE v1 spec's own published
// example, plus an empty-payload case, using literal expected byte strings
// (not a call to PAE or fmt.Appendf) so a shared mistake in the formula
// cannot pass both this test and the one that recomputes it.
func TestPAESpecVector(t *testing.T) {
	got := PAE("http://example.com/HelloWorld", []byte("hello world"))
	want := "DSSEv1 29 http://example.com/HelloWorld 11 hello world"
	if string(got) != want {
		t.Errorf("PAE(spec example) = %q, want %q", got, want)
	}

	got = PAE(PayloadType, []byte(""))
	want = "DSSEv1 28 application/vnd.in-toto+json 0 "
	if string(got) != want {
		t.Errorf("PAE(empty payload) = %q, want %q", got, want)
	}
}
