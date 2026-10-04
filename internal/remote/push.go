package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/emeland-io/custos/internal/gitrepo"
)

// Push uploads the attachments referenced in the commits that remoteName
// does not have yet and that the server is missing, then runs
// git push remoteName args... Attachments are read from dir/BlobDir. When
// an attachment is neither there nor on the server, or its local content
// does not match its hash, nothing is pushed.
func Push(dir, remoteName string, args []string, author gitrepo.Signature, log io.Writer) error {
	repoURL, err := git(dir, "remote", "get-url", remoteName)
	if err != nil {
		return err
	}
	server, err := ServerURL(repoURL)
	if err != nil {
		return err
	}
	if revs := sources(args); len(revs) > 0 {
		revList := append([]string{"rev-list"}, revs...)
		out, err := git(dir, append(revList, "--not", "--remotes="+remoteName, "--")...)
		if err != nil {
			return err
		}
		// Self-heal: also check the tip trees of the refs being pushed, not
		// only the commits rev-list finds new. A ref that reached the
		// remote once already (a plain git push, or an earlier custos push
		// that failed after git push ran) leaves rev-list with nothing to
		// report, but its attachments may still be missing on the server.
		tips, err := tipRevs(dir, revs)
		if err != nil {
			return err
		}
		ats, err := attachments(dir, append(strings.Fields(out), tips...))
		if err != nil {
			return err
		}
		for _, at := range ats {
			if err := ensureUploaded(dir, server, at, author, log); err != nil {
				return err
			}
		}
	}
	cmd := exec.Command("git", append([]string{"-C", dir, "push", remoteName}, args...)...)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}

// bulkRevListFlags maps a git push flag that selects many refs at once to
// the git rev-list flag that selects the same commits.
var bulkRevListFlags = map[string]string{
	"--all":      "--branches",
	"--branches": "--branches",
	"--mirror":   "--all",
	"--tags":     "--tags",
}

// valueFlags are git push flags whose value is a separate argument (or
// follows "=" in the same argument), never a refspec.
var valueFlags = map[string]bool{
	"-o": true, "--push-option": true,
	"--repo": true, "--receive-pack": true, "--exec": true,
}

// sources returns the git rev-list arguments that select the commits a
// push with args would send: the local side of each refspec, or the
// rev-list equivalent of a bulk flag (--all/--branches, --mirror, --tags).
// HEAD when args select nothing explicitly, as git push does then.
func sources(args []string) []string {
	var revs []string
	selected := false
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if valueFlags[arg] {
			skipNext = true // the next argument is this flag's value
			continue
		}
		if name, _, ok := strings.Cut(arg, "="); ok && valueFlags[name] {
			continue // the --flag=value form
		}
		if rl, ok := bulkRevListFlags[arg]; ok {
			revs = append(revs, rl)
			selected = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		selected = true
		src, _, _ := strings.Cut(strings.TrimPrefix(arg, "+"), ":")
		if src != "" {
			revs = append(revs, src)
		}
	}
	if !selected {
		return []string{"HEAD"}
	}
	return revs
}

// tipRevs resolves revs (as sources returns them, possibly including
// rev-list flags such as --branches) to the concrete commit hashes they
// name, for the self-heal check of tip trees in Push.
func tipRevs(dir string, revs []string) ([]string, error) {
	var tips []string
	for _, rev := range revs {
		out, err := git(dir, "rev-parse", rev)
		if err != nil {
			return nil, err
		}
		tips = append(tips, strings.Fields(out)...)
	}
	return tips, nil
}

// ensureUploaded uploads one attachment unless the server has it.
func ensureUploaded(dir, server string, at attachment, author gitrepo.Signature, log io.Writer) error {
	ok, err := onServer(server, at.sha)
	if err != nil || ok {
		return err
	}
	f, err := os.Open(localBlob(dir, at.sha))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("attachment %q in %s: blob %s is neither in %s nor on the server; nothing was pushed",
			at.name, at.path, at.sha, BlobDir)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	// Hash the local file before uploading it: a mismatch is refused here,
	// with nothing sent to the server.
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != at.sha {
		return fmt.Errorf("attachment %q: %s holds content with hash %s, not %s; nothing was pushed",
			at.name, localBlob(dir, at.sha), got, at.sha)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, server+"/api/blobs", f)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(authorHeader, author.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&body) // the status decides; the body explains
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload attachment %q: %s: %s; nothing was pushed", at.name, resp.Status, body.Error)
	}
	fmt.Fprintf(log, "uploaded attachment %q (%s)\n", at.name, at.sha)
	return nil
}

// onServer reports whether the server has a blob.
func onServer(server, sha string) (bool, error) {
	resp, err := http.Head(server + "/api/blobs/" + sha)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	}
	return false, fmt.Errorf("check blob %s: %s", sha, resp.Status)
}
