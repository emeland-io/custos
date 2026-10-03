// Package remote implements custos clone and custos push: git, plus the
// attachments that live outside git in the server's blob store (spec §3.4,
// §6.2, ruling 2.7). A checkout keeps attachments in BlobDir.
package remote

import (
	"bytes"
	"fmt"
	"maps"
	"net/url"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/workspace"
)

// BlobDir is where a checkout keeps attachments, relative to its root.
// Clone keeps .custos/ out of git status through .git/info/exclude.
const BlobDir = ".custos/blobs/sha256"

const authorHeader = "X-Custos-Author"

// ServerURL returns the base URL of the custos server that serves the
// repository at repoURL: everything before "/git/".
func ServerURL(repoURL string) (string, error) {
	u, err := url.Parse(repoURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not an http(s) URL; custos serves repositories at <server>/git/...", repoURL)
	}
	i := strings.Index(u.Path, "/git/")
	if i < 0 {
		return "", fmt.Errorf("%q is not a custos repository URL: its path must contain /git/", repoURL)
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = u.Path[:i], "", "", ""
	return u.String(), nil
}

// GitAuthor returns the person configured as user.name and user.email in
// the checkout dir.
func GitAuthor(dir string) (gitrepo.Signature, error) {
	const hint = `set git config user.name and user.email, or pass --author "Name <email>"`
	if _, err := git(dir, "rev-parse", "--git-dir"); err != nil {
		return gitrepo.Signature{}, fmt.Errorf("%s is not a git checkout; %s", dir, hint)
	}
	name, err := git(dir, "config", "user.name")
	if err != nil {
		return gitrepo.Signature{}, fmt.Errorf("user.name is not set; %s", hint)
	}
	email, err := git(dir, "config", "user.email")
	if err != nil {
		return gitrepo.Signature{}, fmt.Errorf("user.email is not set; %s", hint)
	}
	return gitrepo.ParseSignature(name + " <" + email + ">")
}

// git runs git in dir and returns its trimmed standard output.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// attachment is one blob an answer references.
type attachment struct {
	name, path, sha string // attachment name, answer file, blob hash
}

// attachments lists the blobs referenced by answers in the given revisions
// of the checkout dir, once per hash, sorted by hash. Answers are read
// without validation: the server validates, only the references matter here.
func attachments(dir string, revs []string) ([]attachment, error) {
	repo := &gitrepo.Repo{Dir: dir}
	seen := map[string]attachment{}
	for _, rev := range revs {
		fsys, err := repo.TreeFS(rev)
		if err != nil {
			return nil, err
		}
		w, _ := workspace.Load(fsys)
		for _, path := range slices.Sorted(maps.Keys(w.Answers)) {
			for _, at := range w.Answers[path].Attachments {
				if _, ok := seen[at.SHA256]; !ok && blobs.ValidSHA(at.SHA256) {
					seen[at.SHA256] = attachment{name: at.Name, path: path, sha: at.SHA256}
				}
			}
		}
	}
	out := slices.Collect(maps.Values(seen))
	slices.SortFunc(out, func(a, b attachment) int { return strings.Compare(a.sha, b.sha) })
	return out, nil
}

// localBlob is the path of a blob in the checkout dir.
func localBlob(dir, sha string) string {
	return filepath.Join(dir, filepath.FromSlash(BlobDir), sha)
}
