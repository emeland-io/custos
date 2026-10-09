package match

import (
	"math"
	"slices"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

// chain is a workspace whose generated tasks form the chain
// TaskB → gid(1) → gid(2) → gid(3), plus gid(4) made from TaskA's answer.
func chain() map[string]string {
	return with(emptyWorkspace(), map[string]string{
		genPath(gid(1), "1.0.0"): genFile(gid(1), "1.0.0", "one", fixture.TaskB),
		genPath(gid(2), "1.0.0"): genFile(gid(2), "1.0.0", "two", gid(1)),
		genPath(gid(3), "1.0.0"): genFile(gid(3), "1.0.0", "three", gid(2)),
		genPath(gid(4), "1.0.0"): genFile(gid(4), "1.0.0", "four", fixture.TaskA),
	})
}

func TestDepth(t *testing.T) {
	files := chain()
	valid(t, files)
	w := load(t, files)
	for _, c := range []struct {
		id   string
		want int
	}{
		{gid(1), 1}, {gid(2), 2}, {gid(3), 3}, {gid(4), 1},
		{fixture.TaskB, -1}, // a catalog task is not in the workspace
		{gid(99), -1},
	} {
		if got := Depth(w, c.id); got != c.want {
			t.Errorf("Depth(%s) = %d, want %d", c.id, got, c.want)
		}
	}
}

func TestDepthCycle(t *testing.T) {
	w := load(t, with(emptyWorkspace(), map[string]string{
		genPath(gid(5), "1.0.0"): genFile(gid(5), "1.0.0", "five", gid(6)),
		genPath(gid(6), "1.0.0"): genFile(gid(6), "1.0.0", "six", gid(5)),
		genPath(gid(7), "1.0.0"): genFile(gid(7), "1.0.0", "seven", gid(6)),
	}))
	for _, id := range []string{gid(5), gid(6), gid(7)} {
		if got := Depth(w, id); got != math.MaxInt {
			t.Errorf("Depth(%s) = %d, want math.MaxInt for a cycle", id, got)
		}
	}
}

func TestDepthFollowsCurrentVersion(t *testing.T) {
	// Version 1.0.0 of gid(2) was made from TaskA's answer, the current
	// version 1.1.0 from gid(1)'s.
	w := load(t, with(emptyWorkspace(), map[string]string{
		genPath(gid(1), "1.0.0"): genFile(gid(1), "1.0.0", "one", fixture.TaskB),
		genPath(gid(2), "1.0.0"): genFile(gid(2), "1.0.0", "two", fixture.TaskA),
		genPath(gid(2), "1.1.0"): genFile(gid(2), "1.1.0", "two", gid(1), gid(2)+"@1.0.0"),
	}))
	if got := Depth(w, gid(2)); got != 2 {
		t.Errorf("Depth = %d, want 2", got)
	}
	if g := Current(w, gid(2)); g == nil || g.Version != "1.1.0" {
		t.Errorf("Current = %+v, want version 1.1.0", g)
	}
	if g := Current(w, gid(99)); g != nil {
		t.Errorf("Current of an unknown task = %+v, want nil", g)
	}
}

func TestCurrentWithTwoHeads(t *testing.T) {
	w := load(t, with(emptyWorkspace(), map[string]string{
		genPath(gid(1), "1.0.0"): genFile(gid(1), "1.0.0", "one", fixture.TaskB),
		genPath(gid(1), "2.0.0"): genFile(gid(1), "2.0.0", "one", fixture.TaskB),
	}))
	if g := Current(w, gid(1)); g == nil || g.Version != "2.0.0" {
		t.Errorf("Current = %+v, want the highest version 2.0.0", g)
	}
}

func TestBelow(t *testing.T) {
	w := load(t, chain())
	for _, c := range []struct {
		id   string
		want []string
	}{
		{fixture.TaskB, []string{gid(1), gid(2), gid(3)}},
		{gid(1), []string{gid(2), gid(3)}},
		{gid(3), nil},
		{fixture.TaskA, []string{gid(4)}},
		{gid(99), nil},
	} {
		if got := Below(w, c.id); !slices.Equal(got, c.want) {
			t.Errorf("Below(%s) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestBelowCycle(t *testing.T) {
	w := load(t, with(emptyWorkspace(), map[string]string{
		genPath(gid(5), "1.0.0"): genFile(gid(5), "1.0.0", "five", gid(6)),
		genPath(gid(6), "1.0.0"): genFile(gid(6), "1.0.0", "six", gid(5)),
	}))
	if got := Below(w, gid(5)); !slices.Equal(got, []string{gid(6)}) {
		t.Errorf("Below = %v, want [%s]", got, gid(6))
	}
}
