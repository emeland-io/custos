package match

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/fixture"
)

// envelopeVerifier treats {"payload": P, "sig": S} as a signed envelope
// with payload P, verified as signer "key-S"; anything else is unsigned
// with the canonical content as payload. Re-signing changes S, not P.
type envelopeVerifier struct{}

func (envelopeVerifier) Verify(content []byte) attest.Result {
	var env struct {
		Payload json.RawMessage `json:"payload"`
		Sig     string          `json:"sig"`
	}
	if err := json.Unmarshal(content, &env); err != nil || env.Sig == "" {
		return attest.Unverified.Verify(content)
	}
	p, _ := attest.Canonical(env.Payload)
	return attest.Result{Status: attest.StatusVerified, Signers: []string{"key-" + env.Sig}, Payload: p}
}

// docPath returns the path of document id.
func docPath(id string) string { return "documents/" + id + ".json" }

// slsaDoc is the output document that reproduces fixture.DocumentFile.
func slsaDoc() contract.OutputDocument {
	return contract.OutputDocument{MatchKey: "slsa:web-01", Name: "provenance", MediaType: "application/vnd.in-toto+json",
		Content: json.RawMessage(`{ "_type": "https://in-toto.io/Statement/v1" }`)}
}

// signedDocWorkspace is emptyWorkspace with a document of TaskB's answer
// whose content is an envelope signed with sig.
func signedDocWorkspace(sig string) map[string]string {
	return with(emptyWorkspace(), map[string]string{docPath(fixture.DocID): `{"match_key":"att","name":"att","media_type":"application/json",` +
		`"produced_by":{"task":{"id":"` + fixture.TaskB + `","version":"1.0.0"},"processor":"host-scanner","digest":"` + digest +
		`","answer_commit":"` + fixture.Commit + `"},"verification":{"status":"verified","signers":["key-` + sig + `"]},` +
		`"content":{"payload":{"subject":"web-01"},"sig":"` + sig + `"}}`})
}

func TestPlanAddsDocument(t *testing.T) {
	files := emptyWorkspace()
	doc := contract.OutputDocument{MatchKey: "sbom", Name: "SBOM <web-01>", MediaType: "application/json",
		Content: json.RawMessage(`{"components":[1,2]}`)}
	ch, err := Plan(newRun(t, files), &contract.Output{Documents: []contract.OutputDocument{doc}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items, Item{Kind: KindDocument, MatchKey: "sbom", Action: Added, ID: gid(100), Title: "SBOM <web-01>"})
	if v := ch.Items[0].Verification; v == nil || v.Status != attest.StatusUnsigned {
		t.Errorf("verification %+v, want unsigned", v)
	}
	if got := paths(ch.Files); !slices.Equal(got, []string{"+" + docPath(gid(100))}) {
		t.Fatalf("files %v", got)
	}
	want := `{
  "match_key": "sbom",
  "name": "SBOM <web-01>",
  "media_type": "application/json",
  "produced_by": {
    "task": {
      "id": "` + fixture.TaskB + `",
      "version": "1.0.0"
    },
    "processor": "host-scanner",
    "digest": "` + digest + `",
    "answer_commit": "` + fixture.Commit + `"
  },
  "verification": {
    "status": "unsigned"
  },
  "content": {
    "components": [
      1,
      2
    ]
  }
}
`
	if got := string(ch.Files[0].Data); got != want {
		t.Errorf("file:\n%s\nwant:\n%s", got, want)
	}
	valid(t, apply(files, ch.Files))
}

func TestPlanUnchangedDocument(t *testing.T) {
	// fixture.DocumentFile has the same statement without the spaces.
	ch, err := Plan(newRun(t, fixture.Workspace()), &contract.Output{
		Tasks:     []contract.OutputTask{webTask()},
		Documents: []contract.OutputDocument{slsaDoc()},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindDocument, MatchKey: "slsa:web-01", Action: Unchanged, ID: fixture.DocID, Title: "provenance"},
		Item{Kind: KindTask, MatchKey: "host:web-01", Action: Unchanged, ID: fixture.TaskC, From: "1.0.0", To: "1.0.0", Title: "Host web-01"},
	)
	if len(ch.Files) != 0 {
		t.Errorf("files %v, want none", paths(ch.Files))
	}
}

func TestPlanResignedDocumentIsUnchanged(t *testing.T) {
	run := newRun(t, signedDocWorkspace("1"))
	run.Verifier = envelopeVerifier{}
	doc := contract.OutputDocument{MatchKey: "att", Name: "att", MediaType: "application/json",
		Content: json.RawMessage(`{"sig":"2","payload":{"subject":"web-01"}}`)}
	ch, err := Plan(run, &contract.Output{Documents: []contract.OutputDocument{doc}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items, Item{Kind: KindDocument, MatchKey: "att", Action: Unchanged, ID: fixture.DocID, Title: "att"})
	if len(ch.Files) != 0 {
		t.Errorf("files %v, want none", paths(ch.Files))
	}
}

func TestPlanChangedDocumentKeepsItsFile(t *testing.T) {
	for name, c := range map[string]struct {
		doc    contract.OutputDocument
		signer string
	}{
		"payload":    {contract.OutputDocument{MatchKey: "att", Name: "att", MediaType: "application/json", Content: json.RawMessage(`{"payload":{"subject":"db-01"},"sig":"2"}`)}, "key-2"},
		"name":       {contract.OutputDocument{MatchKey: "att", Name: "renamed", MediaType: "application/json", Content: json.RawMessage(`{"payload":{"subject":"web-01"},"sig":"1"}`)}, "key-1"},
		"media type": {contract.OutputDocument{MatchKey: "att", Name: "att", MediaType: "application/vnd.in-toto+json", Content: json.RawMessage(`{"payload":{"subject":"web-01"},"sig":"1"}`)}, "key-1"},
	} {
		t.Run(name, func(t *testing.T) {
			files := signedDocWorkspace("1")
			run := newRun(t, files)
			run.Verifier = envelopeVerifier{}
			ch, err := Plan(run, &contract.Output{Documents: []contract.OutputDocument{c.doc}})
			if err != nil {
				t.Fatal(err)
			}
			wantItems(t, ch.Items, Item{Kind: KindDocument, MatchKey: "att", Action: NewVersion, ID: fixture.DocID, Title: c.doc.Name})
			if got := paths(ch.Files); !slices.Equal(got, []string{"+" + docPath(fixture.DocID)}) {
				t.Fatalf("files %v", got)
			}
			after := apply(files, ch.Files)
			valid(t, after)
			d := load(t, after).Documents[docPath(fixture.DocID)]
			if d.Verification == nil || d.Verification.Status != attest.StatusVerified || !slices.Equal(d.Verification.Signers, []string{c.signer}) {
				t.Errorf("verification %+v, want verified by %s", d.Verification, c.signer)
			}
			if d.Name != c.doc.Name || d.MediaType != c.doc.MediaType {
				t.Errorf("name %q, media type %q", d.Name, d.MediaType)
			}
		})
	}
}

func TestPlanRemovesDocument(t *testing.T) {
	files := fixture.Workspace()
	ch, err := Plan(newRun(t, files), &contract.Output{Tasks: []contract.OutputTask{webTask()}})
	if err != nil {
		t.Fatal(err)
	}
	wantItems(t, ch.Items,
		Item{Kind: KindDocument, MatchKey: "slsa:web-01", Action: Removed, ID: fixture.DocID, Title: "provenance"},
		Item{Kind: KindTask, MatchKey: "host:web-01", Action: Unchanged, ID: fixture.TaskC, From: "1.0.0", To: "1.0.0", Title: "Host web-01"},
	)
	if ch.Items[0].Verification != nil {
		t.Errorf("removed item has verification %+v", ch.Items[0].Verification)
	}
	if got := paths(ch.Files); !slices.Equal(got, []string{"-" + docPath(fixture.DocID)}) {
		t.Errorf("files %v", got)
	}
}

func TestPlanDocumentsOfOtherAnswersStay(t *testing.T) {
	files := fixture.Workspace() // its document was made from TaskB's answer
	run := newRun(t, files)
	run.Task.ID = fixture.TaskA
	ch, err := Plan(run, &contract.Output{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Items) != 0 || len(ch.Files) != 0 {
		t.Errorf("items:\n%sfiles %v", fmtItems(ch.Items), paths(ch.Files))
	}
}
