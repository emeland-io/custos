package remote

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

// errUnavailable means the server does not have a blob.
var errUnavailable = errors.New("blob unavailable")

// Clone clones repoURL into dir with git and downloads the attachments that
// the answers on the checked-out branch reference into dir/BlobDir. An
// attachment the server does not have is reported on log and skipped
// (spec §7); the clone still succeeds. git's own output also goes to log.
func Clone(repoURL, dir string, log io.Writer) error {
	server, err := ServerURL(repoURL)
	if err != nil {
		return err
	}
	cmd := exec.Command("git", "clone", "--", repoURL, dir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	if err := excludeBlobDir(dir); err != nil {
		return err
	}
	return fetchBlobs(dir, server, log)
}

// excludeBlobDir keeps .custos/ out of git status and git add -A.
func excludeBlobDir(dir string) error {
	path, err := git(dir, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.WriteString("\n/.custos/\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// fetchBlobs downloads the attachments of the answers in HEAD that are not
// in dir/BlobDir yet.
func fetchBlobs(dir, server string, log io.Writer) error {
	if _, err := git(dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return nil // an empty repository has no answers yet
	}
	ats, err := attachments(dir, []string{"HEAD"})
	if err != nil {
		return err
	}
	for _, at := range ats {
		dest := localBlob(dir, at.sha)
		if _, err := os.Stat(dest); err == nil {
			continue
		}
		err := download(server, at.sha, dest)
		if errors.Is(err, errUnavailable) {
			fmt.Fprintf(log, "attachment %q in %s is unavailable: the server has no blob %s\n", at.name, at.path, at.sha)
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// download fetches one blob into dest, checking its hash before it appears.
func download(server, sha, dest string) error {
	resp, err := http.Get(server + "/api/blobs/" + sha)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errUnavailable
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("download blob %s: %s", sha, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name()) // fails harmlessly after the rename
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download blob %s: %w", sha, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sha {
		return fmt.Errorf("download blob %s: the server sent content with hash %s", sha, got)
	}
	return os.Rename(f.Name(), dest)
}
