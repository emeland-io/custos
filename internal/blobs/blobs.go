// Package blobs stores attachments by content: one file per SHA-256 hash
// below <dir>/sha256, shared by all workspaces (ruling 2.7). Blobs never
// change and are never deleted, so readers need no locks.
package blobs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// MaxSize is the largest blob Put accepts (ruling 2.8).
const MaxSize = 1 << 30

// ErrTooLarge is returned by Put for content larger than MaxSize.
var ErrTooLarge = errors.New("blob too large")

var shaRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidSHA reports whether s is a SHA-256 digest in 64 lowercase hex digits.
func ValidSHA(s string) bool { return shaRE.MatchString(s) }

// Store is a content-addressed blob store in a directory:
//
//	sha256/<hash>   the blobs, read-only
//	tmp/            uploads in progress, on the same file system for rename
type Store struct {
	dir   string
	limit int64 // MaxSize; tests lower it
}

// Open opens the blob store in dir, creating its directories.
func Open(dir string) (*Store, error) {
	for _, sub := range []string{"sha256", "tmp"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir, limit: MaxSize}, nil
}

// Put stores the content of r and returns its hash and size. The blob
// appears under its hash only once it is complete, and storing the same
// content again changes nothing.
func (s *Store) Put(r io.Reader) (sha string, size int64, err error) {
	f, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "put-*")
	if err != nil {
		return "", 0, err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(r, s.limit+1))
	if err != nil {
		return "", 0, err
	}
	if size > s.limit {
		return "", 0, ErrTooLarge
	}
	if err = f.Sync(); err != nil {
		return "", 0, err
	}
	if err = f.Close(); err != nil {
		return "", 0, err
	}
	sha = hex.EncodeToString(h.Sum(nil))
	if s.Has(sha) {
		os.Remove(f.Name())
		return sha, size, nil
	}
	if err = os.Chmod(f.Name(), 0o444); err != nil {
		return "", 0, err
	}
	if err = os.Rename(f.Name(), s.path(sha)); err != nil {
		return "", 0, err
	}
	return sha, size, nil
}

// Open returns the blob with hash sha and its size. An unknown or malformed
// hash gives an error that matches fs.ErrNotExist.
func (s *Store) Open(sha string) (io.ReadCloser, int64, error) {
	if !ValidSHA(sha) {
		return nil, 0, fmt.Errorf("blob %q: %w", sha, fs.ErrNotExist)
	}
	f, err := os.Open(s.path(sha))
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// Has reports whether the blob with hash sha is stored.
func (s *Store) Has(sha string) bool {
	if !ValidSHA(sha) {
		return false
	}
	_, err := os.Stat(s.path(sha))
	return err == nil
}

func (s *Store) path(sha string) string { return filepath.Join(s.dir, "sha256", sha) }
