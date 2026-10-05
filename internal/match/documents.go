package match

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/workspace"
)

// documents matches the output documents against the documents the answered
// task produced earlier. A document is unchanged when its payload (as the
// verifier reports it), name and media type are unchanged; re-signing alone
// is not a change (§5.4).
func (p *planner) documents(out []contract.OutputDocument) error {
	earlier, extra := earlierDocuments(p.run.Workspace, p.run.Task.ID)
	for _, d := range out {
		res := p.run.Verifier.Verify(d.Content)
		v := &workspace.Verification{Status: res.Status, Signers: res.Signers}
		old, found := earlier[d.MatchKey]
		delete(earlier, d.MatchKey)
		item := Item{Kind: KindDocument, MatchKey: d.MatchKey, Title: d.Name, Verification: v}
		switch {
		case !found:
			item.Action, item.ID = Added, p.run.NewID()
		case old.Name == d.Name && old.MediaType == d.MediaType &&
			bytes.Equal(p.run.Verifier.Verify(old.Content).Payload, res.Payload):
			item.Action, item.ID = Unchanged, documentID(old.Path)
			p.items = append(p.items, item)
			continue
		default:
			item.Action, item.ID = NewVersion, documentID(old.Path)
		}
		data, err := encodeDocument(workspace.Document{
			MatchKey: d.MatchKey, Name: d.Name, MediaType: d.MediaType,
			ProducedBy: p.produced, Verification: v, Content: d.Content,
		})
		if err != nil {
			return fmt.Errorf("document %q: %w", d.MatchKey, err)
		}
		p.write("documents/"+item.ID+".json", data)
		p.items = append(p.items, item)
	}
	gone := slices.Collect(maps.Values(earlier))
	for _, d := range append(gone, extra...) {
		p.remove(d.Path)
		p.items = append(p.items, Item{Kind: KindDocument, MatchKey: d.MatchKey, Action: Removed, ID: documentID(d.Path), Title: d.Name})
	}
	return nil
}

// earlierDocuments returns the documents the answered task produced
// earlier, by match key. When two share a key (only possible after hand
// edits), the one with the smallest path is matched and the others are
// returned in extra, to be removed.
func earlierDocuments(w *workspace.Workspace, taskID string) (byKey map[string]*workspace.Document, extra []*workspace.Document) {
	byKey = map[string]*workspace.Document{}
	for _, path := range slices.Sorted(maps.Keys(w.Documents)) {
		d := w.Documents[path]
		if d.ProducedBy.Task.ID != taskID {
			continue
		}
		if _, dup := byKey[d.MatchKey]; dup {
			extra = append(extra, d)
			continue
		}
		byKey[d.MatchKey] = d
	}
	return byKey, extra
}

// documentID returns the uuid in documents/<uuid>.json.
func documentID(path string) string {
	return strings.TrimSuffix(strings.TrimPrefix(path, "documents/"), ".json")
}

// encodeDocument writes a document file: indented JSON without HTML
// escaping, ending in a newline.
func encodeDocument(d workspace.Document) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
