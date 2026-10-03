// Package gittest runs git in tests, isolated from the user's git
// configuration. Only test code imports it.
package gittest

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
)

// Env is the environment for git commands in tests.
func Env() []string {
	return append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.org",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.org",
	)
}

// Try runs git in dir and returns its combined output.
func Try(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = Env()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Run runs git in dir and fails the test if it fails.
func Run(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := Try(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(out)
}

// Init creates a work repository with branch main.
func Init(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	Run(t, dir, "init", "--quiet", "--initial-branch=main")
	return dir
}

// Commit writes files into dir, commits all changes and returns the commit ID.
func Commit(t testing.TB, dir string, files map[string]string) string {
	t.Helper()
	fixture.WriteDir(t, dir, files)
	Run(t, dir, "add", "-A")
	Run(t, dir, "commit", "--quiet", "--allow-empty", "-m", "test")
	return Run(t, dir, "rev-parse", "HEAD")
}
