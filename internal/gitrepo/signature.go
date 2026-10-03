package gitrepo

import (
	"errors"
	"fmt"
	"strings"
)

// Signature names the author or committer of a commit.
type Signature struct{ Name, Email string }

// Bot is the committer of every commit custos writes, and the author of the
// commits it makes on its own.
var Bot = Signature{Name: "custos-bot", Email: "custos-bot@localhost"}

// ParseSignature parses "Jane Doe <jane@example.org>".
func ParseSignature(s string) (Signature, error) {
	s = strings.TrimSpace(s)
	open := strings.LastIndex(s, "<")
	if open < 0 || !strings.HasSuffix(s, ">") {
		return Signature{}, fmt.Errorf("%q is not of the form Name <email>", s)
	}
	sig := Signature{Name: strings.TrimSpace(s[:open]), Email: s[open+1 : len(s)-1]}
	if err := sig.check(); err != nil {
		return Signature{}, err
	}
	return sig, nil
}

// String returns the signature as "Name <email>".
func (s Signature) String() string { return s.Name + " <" + s.Email + ">" }

func (s Signature) check() error {
	if s.Name == "" || s.Email == "" {
		return errors.New("a signature needs a name and an email, as in Jane Doe <jane@example.org>")
	}
	if strings.ContainsAny(s.Name, "<>\n\r\x00") || strings.ContainsAny(s.Email, "<>\n\r\x00 \t") {
		return fmt.Errorf("%q: name and email must not contain <, > or line breaks, and the email no spaces", s.String())
	}
	return nil
}
