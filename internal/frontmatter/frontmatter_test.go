package frontmatter

import (
	"testing"
)

func TestSplit(t *testing.T) {
	tests := []struct {
		name, in, meta, body string
		wantErr              bool
	}{
		{name: "basic", in: "---\na: 1\n---\n\nbody\n", meta: "a: 1\n", body: "body\n"},
		{name: "windows line endings", in: "---\r\na: 1\r\n---\r\n\r\nbody\r\n", meta: "a: 1\n", body: "body\n"},
		{name: "no final newline", in: "---\na: 1\n---", meta: "a: 1\n", body: ""},
		{name: "empty frontmatter", in: "---\n---\nbody\n", meta: "", body: "body\n"},
		{name: "body contains a rule", in: "---\na: 1\n---\nx\n---\ny\n", meta: "a: 1\n", body: "x\n---\ny\n"},
		{name: "missing start", in: "a: 1\n", wantErr: true},
		{name: "missing end", in: "---\na: 1\n", wantErr: true},
		{name: "empty file", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta, body, err := Split([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(meta) != tt.meta || body != tt.body {
				t.Errorf("got meta %q body %q, want %q %q", meta, body, tt.meta, tt.body)
			}
		})
	}
}

type sample struct {
	A int      `yaml:"a"`
	B []string `yaml:"b,omitempty"`
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	var s sample
	if _, err := Decode([]byte("---\na: 1\nc: 2\n---\n"), &s); err == nil {
		t.Fatal("want error for unknown field c")
	}
}

func TestDecodeEmptyFrontmatter(t *testing.T) {
	s := sample{A: 7}
	body, err := Decode([]byte("---\n---\nhello\n"), &s)
	if err != nil {
		t.Fatal(err)
	}
	if s.A != 7 || body != "hello\n" {
		t.Errorf("got %+v %q", s, body)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	in := sample{A: 1, B: []string{"x", "z"}}
	data, err := Encode(in, "Body text.\n")
	if err != nil {
		t.Fatal(err)
	}
	want := "---\na: 1\nb:\n  - x\n  - z\n---\n\nBody text.\n"
	if string(data) != want {
		t.Fatalf("got\n%s\nwant\n%s", data, want)
	}
	var out sample
	body, err := Decode(data, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.A != 1 || len(out.B) != 2 || out.B[0] != "x" || out.B[1] != "z" || body != "Body text.\n" {
		t.Errorf("round trip got %+v %q", out, body)
	}
}
