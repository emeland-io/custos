package api

import (
	"cmp"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/task"
)

func TestCreateAndListWorkspaces(t *testing.T) {
	e := newEnv(t, fixture.Catalog())
	if got := decode[[]workspaceJSON](t, e.do(t, "GET", "/api/workspaces", "", ""), http.StatusOK); got == nil || len(got) != 0 {
		t.Fatalf("empty list: %#v", got)
	}

	rec := e.do(t, "POST", "/api/workspaces", janeHeader, `{"id":"`+fixture.WorkspaceID+`"}`)
	if got := decode[workspaceIDJSON](t, rec, http.StatusCreated); got.ID != fixture.WorkspaceID {
		t.Fatalf("created %q", got.ID)
	}
	generated := decode[workspaceIDJSON](t, e.do(t, "POST", "/api/workspaces", janeHeader, ""), http.StatusCreated)
	if !task.ValidID(generated.ID) {
		t.Fatalf("generated id %q is not a UUID v4", generated.ID)
	}

	want := []workspaceJSON{
		{ID: fixture.WorkspaceID, Pin: e.catalogCommit},
		{ID: generated.ID, Pin: e.catalogCommit},
	}
	slices.SortFunc(want, func(a, b workspaceJSON) int { return cmp.Compare(a.ID, b.ID) })
	got := decode[[]workspaceJSON](t, e.do(t, "GET", "/api/workspaces", "", ""), http.StatusOK)
	if !slices.Equal(got, want) {
		t.Errorf("list %+v, want %+v", got, want)
	}
}

func TestCreateWorkspaceErrors(t *testing.T) {
	e := newEnv(t, fixture.Catalog())
	body := `{"id":"` + fixture.WorkspaceID + `"}`
	decode[workspaceIDJSON](t, e.do(t, "POST", "/api/workspaces", janeHeader, body), http.StatusCreated)

	tests := []struct {
		name, author, body string
		code               int
	}{
		{"exists", janeHeader, body, http.StatusConflict},
		{"no author", "", `{"id":"` + otherWorkspace + `"}`, http.StatusUnauthorized},
		{"bad author", "Jane", `{"id":"` + otherWorkspace + `"}`, http.StatusUnauthorized},
		{"path in id", janeHeader, `{"id":"../escape"}`, http.StatusBadRequest},
		{"id of wrong type", janeHeader, `{"id":5}`, http.StatusBadRequest},
		{"unknown field", janeHeader, `{"name":"x"}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decode[errorJSON](t, e.do(t, "POST", "/api/workspaces", tt.author, tt.body), tt.code)
		})
	}
	if ids, err := e.st.WorkspaceIDs(); err != nil || len(ids) != 1 {
		t.Errorf("workspaces after failed creates: %v %v", ids, err)
	}
}

func TestCreateWorkspaceNeedsCatalog(t *testing.T) {
	e := newEnv(t, nil)
	body := decode[errorJSON](t, e.do(t, "POST", "/api/workspaces", janeHeader, ""), http.StatusConflict)
	if !strings.Contains(body.Error, "catalog") {
		t.Errorf("error %q", body.Error)
	}
}
