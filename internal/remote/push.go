package remote

import (
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
		ats, err := attachments(dir, strings.Fields(out))
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

// sources returns the local side of each refspec in args, skipping flags
// and deletions; HEAD when args name no refspec, as git push does then.
func sources(args []string) []string {
	var revs []string
	refspecs := 0
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		refspecs++
		src, _, _ := strings.Cut(strings.TrimPrefix(arg, "+"), ":")
		if src != "" {
			revs = append(revs, src)
		}
	}
	if refspecs == 0 {
		return []string{"HEAD"}
	}
	return revs
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
	info, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, server+"/api/blobs", f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(authorHeader, author.String())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var body struct {
		SHA256 string `json:"sha256"`
		Error  string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&body) // the status decides; the body explains
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload attachment %q: %s: %s; nothing was pushed", at.name, resp.Status, body.Error)
	}
	if body.SHA256 != at.sha {
		return fmt.Errorf("attachment %q: %s holds content with hash %s, not %s; nothing was pushed",
			at.name, localBlob(dir, at.sha), body.SHA256, at.sha)
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
