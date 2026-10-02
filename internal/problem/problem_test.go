package problem

import (
	"slices"
	"testing"
)

func TestString(t *testing.T) {
	p := Problem{Path: "groups/index.yaml", Rule: RuleGroups, Message: "lists unknown group"}
	if got, want := p.String(), "groups/index.yaml: groups: lists unknown group"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	p.Path = ""
	if got, want := p.String(), "groups: lists unknown group"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestListAndSort(t *testing.T) {
	var l List
	l.Add("b", RulePath, "second %d", 2)
	l.Add("a", RuleFormat, "first")
	Sort(l)
	got := []string{l[0].String(), l[1].String()}
	want := []string{"a: format: first", "b: path: second 2"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
