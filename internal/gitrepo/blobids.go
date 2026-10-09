package gitrepo

import (
	"fmt"
	"strings"
)

// BlobIDs returns the object ids of the regular files below directory dir
// (such as "answers") in revision rev, keyed by path. A missing directory
// gives an empty map. Like TreeFS it reads the tree as stored.
func (r *Repo) BlobIDs(rev, dir string) (map[string]string, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	if err := checkPath(dir); err != nil {
		return nil, err
	}
	out, err := r.git("ls-tree", "-r", "-z", "--full-tree", rev, "--", dir+"/")
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, rec := range splitNUL(out) {
		// <mode> SP <type> SP <oid> TAB <path>
		meta, path, ok := strings.Cut(rec, "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 {
			return nil, fmt.Errorf("unexpected git ls-tree output %q", rec)
		}
		if f[1] == "blob" && (f[0] == "100644" || f[0] == "100755") {
			ids[path] = f[2]
		}
	}
	return ids, nil
}
