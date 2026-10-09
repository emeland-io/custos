package blobs

import "path/filepath"

// Path returns the absolute path of the blob with hash sha, for mounting it
// into a processor container; ok is false when the blob is not stored. The
// file is read-only and never changes.
func (s *Store) Path(sha string) (path string, ok bool) {
	if !s.Has(sha) {
		return "", false
	}
	abs, err := filepath.Abs(s.path(sha))
	if err != nil {
		return "", false
	}
	return abs, true
}
