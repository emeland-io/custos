package gitrepo

import (
	"testing"

	"github.com/emeland-io/custos/internal/gittest"
)

func TestBlobIDs(t *testing.T) {
	dir := gittest.Init(t)
	c := gittest.Commit(t, dir, map[string]string{
		"answers/a.md":  "one",
		"answers/b.md":  "two",
		"answersX/c.md": "not below answers/",
		"other.md":      "x",
	})
	r := &Repo{Dir: dir}
	ids, err := r.BlobIDs(c, "answers")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"answers/a.md": gittest.Run(t, dir, "rev-parse", c+":answers/a.md"),
		"answers/b.md": gittest.Run(t, dir, "rev-parse", c+":answers/b.md"),
	}
	if len(ids) != len(want) || ids["answers/a.md"] != want["answers/a.md"] || ids["answers/b.md"] != want["answers/b.md"] {
		t.Errorf("BlobIDs = %v, want %v", ids, want)
	}
	if ids, err := r.BlobIDs(c, "documents"); err != nil || len(ids) != 0 {
		t.Errorf("missing directory: %v %v", ids, err)
	}
	if _, err := r.BlobIDs("--all", "answers"); err == nil {
		t.Error("options must be rejected")
	}
}
