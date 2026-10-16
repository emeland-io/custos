package catalogdraft

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emeland-io/custos/internal/api"
	"github.com/emeland-io/custos/internal/blobs"
	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
)

// newAPI returns an api.API over st with this package's endpoints
// registered and no auth wrapping (auth.Require is plan 4a's; these tests
// exercise this package's handlers directly, the way internal/merge's own
// http_test.go exercises handleFork/handleMerge directly).
func newAPI(t *testing.T, st *store.Store) *api.API {
	t.Helper()
	bl, err := blobs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, bl)
	Register(a, st, nil)
	return a
}

// newAPIWithOnPublish is newAPI, but wires onPublish so a test can confirm
// it fires exactly on a successful publish and never on a conflict or
// other error (cross-plan review, architecture note ruling 17).
func newAPIWithOnPublish(t *testing.T, st *store.Store, onPublish func()) *api.API {
	t.Helper()
	bl, err := blobs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := api.New(st, bl)
	Register(a, st, onPublish)
	return a
}

// allCaller holds every role for every workspace — this file tests
// catalogdraft's own endpoints, not authorization (auth.Require is plan
// 4a's; found while applying this plan to a scratch copy: doJSON below
// originally sent the pre-4a X-Custos-Author header, which the
// task-author-gated routes this plan adds no longer read at all,
// cascading 401s into every test in this file).
type allCaller struct{ sig gitrepo.Signature }

func (c allCaller) Signature() gitrepo.Signature      { return c.sig }
func (c allCaller) Has(role, workspaceID string) bool { return true }

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(data))
	}
	r = r.WithContext(api.WithCaller(r.Context(), allCaller{sig: gitrepo.Signature{Name: "Alice Example", Email: "alice@example.org"}}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandleDraftLifecycle(t *testing.T) {
	st := setup(t)
	h := newAPI(t, st).Handler()

	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "Rename release"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created draftJSON
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	w = doJSON(t, h, "GET", "/api/catalog/drafts/"+created.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("view: %d %s", w.Code, w.Body)
	}
	var view draftViewJSON
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Draft.ID != created.ID || len(view.Problems) != 0 {
		t.Errorf("view = %+v", view)
	}

	w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+created.ID+"/tasks/"+fixture.TaskA, map[string]any{
		"step": "minor", "title": "New title", "body": "New body.\n", "answer_type": "text",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("task edit: %d %s", w.Code, w.Body)
	}

	w = doJSON(t, h, "GET", "/api/catalog/drafts", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var list []draftJSON
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("list = %v, err %v", list, err)
	}

	w = doJSON(t, h, "POST", "/api/catalog/drafts/"+created.ID+"/publish", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	var result publishResultJSON
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Commit == "" {
		t.Fatalf("publish result %+v, err %v", result, err)
	}

	w = doJSON(t, h, "GET", "/api/catalog/drafts/"+created.ID, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("draft still visible after publish: %d", w.Code)
	}
}

func TestHandleGroupEdit(t *testing.T) {
	st := setup(t)
	h := newAPI(t, st).Handler()
	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "Edit a group"})
	var d draftJSON
	json.Unmarshal(w.Body.Bytes(), &d)

	w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/groups/release", map[string]any{
		"title": "Release (renamed)", "children": []map[string]string{{"task": fixture.TaskB}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("group edit: %d %s", w.Code, w.Body)
	}
	data, ok, err := st.CatalogRepo().ReadFile(d.Commit, "")
	_ = data
	_ = ok
	_ = err
	draft, err := Get(st, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	group, ok, err := st.CatalogRepo().ReadFile(draft.Commit, "groups/release/group.yaml")
	if err != nil || !ok {
		t.Fatalf("group.yaml: ok=%v err=%v", ok, err)
	}
	// 2-space list indentation, matching marshalYAML's SetIndent(2) and
	// the project's own existing fixtures (internal/fixture.go's
	// "children:\n  - task: ..." groups) — found while applying this
	// plan to a scratch copy: the original "want" string here hardcoded
	// 4 spaces, which go.yaml.in/yaml/v3 never actually produces at this
	// indent setting.
	if want := "title: Release (renamed)\nchildren:\n  - task: " + fixture.TaskB + "\n"; string(group) != want {
		t.Errorf("group.yaml = %q, want %q", group, want)
	}
}

func TestHandleGroupInvalidSlug(t *testing.T) {
	st := setup(t)
	h := newAPI(t, st).Handler()
	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "x"})
	var d draftJSON
	json.Unmarshal(w.Body.Bytes(), &d)

	// "../escape" and "a/b" never reach handleGroupEdit's own slugRE
	// check at all: net/http's ServeMux, matching a single-segment
	// {slug} wildcard, 404s "a/b" outright (an embedded "/" cannot be
	// part of one segment) and 307-redirects "../escape" to its
	// lexically-cleaned equivalent before any pattern is even matched —
	// both stronger than a 400 from this handler, just enforced one
	// layer earlier than Review Focus 4 assumed. Found while applying
	// this plan to a scratch copy: the original table asserted 400 for
	// these two and failed every time, against net/http's actual routing
	// behavior, not against a gap in the handler.
	for slug, want := range map[string]int{"../escape": http.StatusTemporaryRedirect, "a/b": http.StatusNotFound} {
		w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/groups/"+slug, map[string]any{"title": "x"})
		if w.Code != want {
			t.Errorf("slug %q: %d %s, want %d (from net/http's own routing, before any handler runs)", slug, w.Code, w.Body, want)
		}
	}
	// These single-segment slugs do reach handleGroupEdit, and must be
	// rejected by its own slugRE check (400), independent of routing.
	for _, slug := range []string{"UPPER", "a.b"} {
		w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/groups/"+slug, map[string]any{"title": "x"})
		if w.Code != http.StatusBadRequest {
			t.Errorf("slug %q: %d %s, want 400", slug, w.Code, w.Body)
		}
	}
	// The mux itself 404s an empty path segment.
	w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/groups/", map[string]any{"title": "x"})
	if w.Code != http.StatusNotFound {
		t.Errorf("empty slug: %d %s, want 404", w.Code, w.Body)
	}
}

func TestHandlePublishConflict(t *testing.T) {
	st := setup(t)
	h := newAPI(t, st).Handler()
	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "first"})
	var first draftJSON
	json.Unmarshal(w.Body.Bytes(), &first)
	w = doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "second"})
	var second draftJSON
	json.Unmarshal(w.Body.Bytes(), &second)

	for _, d := range []draftJSON{first, second} {
		w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/tasks/"+fixture.TaskA, map[string]any{
			"step": "minor", "title": "from " + d.ID, "body": "Body.\n", "answer_type": "text",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("task edit for %s: %d %s", d.ID, w.Code, w.Body)
		}
	}
	if w = doJSON(t, h, "POST", "/api/catalog/drafts/"+first.ID+"/publish", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("first publish: %d %s", w.Code, w.Body)
	}
	w = doJSON(t, h, "POST", "/api/catalog/drafts/"+second.ID+"/publish", map[string]any{})
	if w.Code != http.StatusConflict {
		t.Fatalf("second publish: %d %s, want 409", w.Code, w.Body)
	}
	var cb conflictBody
	if err := json.Unmarshal(w.Body.Bytes(), &cb); err != nil || len(cb.Conflicts) != 1 {
		t.Fatalf("conflict body %+v, err %v", cb, err)
	}
	path := cb.Conflicts[0].Path
	w = doJSON(t, h, "POST", "/api/catalog/drafts/"+second.ID+"/publish", map[string]any{
		"resolutions": map[string]any{path: map[string]string{"side": "theirs"}},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("resolved publish: %d %s", w.Code, w.Body)
	}
}

// TestHandlePublishCallsOnPublish: onPublish fires exactly once per
// successful publish, and not at all for the 409 a conflicting publish
// answers first (architecture note ruling 17; cross-plan review).
func TestHandlePublishCallsOnPublish(t *testing.T) {
	st := setup(t)
	calls := 0
	h := newAPIWithOnPublish(t, st, func() { calls++ }).Handler()
	w := doJSON(t, h, "POST", "/api/catalog/drafts", map[string]string{"title": "d"})
	var d draftJSON
	json.Unmarshal(w.Body.Bytes(), &d)
	if w = doJSON(t, h, "PUT", "/api/catalog/drafts/"+d.ID+"/tasks/"+fixture.TaskA, map[string]any{
		"step": "minor", "title": "t", "body": "Body.\n", "answer_type": "text",
	}); w.Code != http.StatusOK {
		t.Fatalf("task edit: %d %s", w.Code, w.Body)
	}
	if calls != 0 {
		t.Fatalf("onPublish called %d times before any publish", calls)
	}
	if w = doJSON(t, h, "POST", "/api/catalog/drafts/"+d.ID+"/publish", map[string]any{}); w.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", w.Code, w.Body)
	}
	if calls != 1 {
		t.Fatalf("onPublish called %d times, want 1", calls)
	}
}
