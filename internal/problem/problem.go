// Package problem describes rule violations found in catalog and workspace
// files.
package problem

import (
	"cmp"
	"fmt"
	"slices"
)

// Rule names. The numbers refer to the rules in section 3.1 of the design.
const (
	RuleFormat        = "format"         // a file cannot be parsed or a field is invalid
	RulePath          = "path"           // rule 4: file stored at the wrong path, or unexpected file
	RuleImmutable     = "immutable"      // rule 1: a published task version changed
	RuleSingleCurrent = "single-current" // rule 2: a task has more than one current version
	RulePrevious      = "previous"       // rule 3: a previous reference is broken
	RuleGroups        = "groups"         // rule 5
	RuleBindings      = "bindings"       // rule 6
	RuleHistory       = "history"        // main was deleted or rewritten
)

// Problem is one rule violation in one file.
type Problem struct {
	Path    string
	Rule    string
	Message string
}

func (p Problem) String() string {
	if p.Path == "" {
		return p.Rule + ": " + p.Message
	}
	return p.Path + ": " + p.Rule + ": " + p.Message
}

// List collects problems.
type List []Problem

// Add appends a problem whose message is formatted like fmt.Sprintf.
func (l *List) Add(path, rule, format string, args ...any) {
	*l = append(*l, Problem{Path: path, Rule: rule, Message: fmt.Sprintf(format, args...)})
}

// Sort orders problems by path, rule and message, for stable output.
func Sort(ps []Problem) {
	slices.SortStableFunc(ps, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Rule, b.Rule), cmp.Compare(a.Message, b.Message))
	})
}
