package repofile

import (
	"regexp"
	"testing"
	"testing/fstest"

	"github.com/emeland-io/custos/internal/problem"
)

var layout = regexp.MustCompile(`^things/[^/]+\.md$`)

func TestWalk(t *testing.T) {
	fsys := fstest.MapFS{
		"things/a.md":      {Data: []byte("A")},
		"things/sub/b.md":  {Data: []byte("B")},
		"things/notes.txt": {Data: []byte("N")},
		"other/c.md":       {Data: []byte("C")},
	}
	var seen []string
	ps := Walk(fsys, "things", layout, "things/<name>.md", func(path string, data []byte) []problem.Problem {
		seen = append(seen, path+"="+string(data))
		return nil
	})
	if len(seen) != 1 || seen[0] != "things/a.md=A" {
		t.Errorf("visited %v", seen)
	}
	problem.Sort(ps)
	if len(ps) != 2 || ps[0].Path != "things/notes.txt" || ps[1].Path != "things/sub/b.md" || ps[0].Rule != problem.RulePath {
		t.Errorf("problems %v", ps)
	}
}

func TestWalkMissingRoot(t *testing.T) {
	if ps := Walk(fstest.MapFS{}, "things", layout, "", nil); len(ps) != 0 {
		t.Errorf("problems %v", ps)
	}
}

type doc struct {
	Name string `yaml:"name"`
}

func TestReadYAML(t *testing.T) {
	fsys := fstest.MapFS{
		"ok.yaml":  {Data: []byte("name: x\n")},
		"bad.yaml": {Data: []byte("name: x\ncolour: red\n")},
	}
	var d doc
	if found, ps := ReadYAML(fsys, "ok.yaml", &d); !found || len(ps) != 0 || d.Name != "x" {
		t.Errorf("ok.yaml: %v %v %+v", found, ps, d)
	}
	if found, ps := ReadYAML(fsys, "missing.yaml", &d); found || len(ps) != 0 {
		t.Errorf("missing.yaml: %v %v", found, ps)
	}
	found, ps := ReadYAML(fsys, "bad.yaml", &d)
	if !found || len(ps) != 1 || ps[0].Path != "bad.yaml" || ps[0].Rule != problem.RuleFormat {
		t.Errorf("bad.yaml: %v %v", found, ps)
	}
}
