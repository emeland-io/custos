package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/fixture"
	"github.com/emeland-io/custos/internal/proctest"
)

var update = flag.Bool("update", false, "rewrite the golden files of processor test")

// golden compares got with testdata/processor-test/<name>.golden, or
// rewrites the file when -update is given.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "processor-test", name+".golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./cmd/custos -run %s -update to create it)", err, t.Name())
	}
	if got != string(want) {
		t.Errorf("output differs from %s:\n--- got\n%s--- want\n%s", path, got, want)
	}
}

// digestOf returns the hex digest of a "<ref>@sha256:<hex>" reference.
func digestOf(ref string) string {
	_, hex, _ := strings.Cut(ref, "@sha256:")
	return hex
}

// normalize replaces what changes from build to build: the image digest
// and, for signed documents, the signer identities.
func normalize(out, hexDigest string) string {
	out = strings.ReplaceAll(out, hexDigest, "<digest>")
	return regexp.MustCompile(`\(verified: [^)]*\)`).ReplaceAllString(out, "(verified: <signers>)")
}

// writeAnswer writes answers/<TaskA>.md below dir and returns its path.
func writeAnswer(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "answers", fixture.TaskA+".md")
	fixture.WriteDir(t, dir, map[string]string{"answers/" + fixture.TaskA + ".md": content})
	return path
}

func markdownAnswer(body string) string {
	return "---\ntask: " + fixture.TaskA + "\ntask_version: 1.0.0\ntype: markdown\n---\n\n" + body
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestProcessorTestUsage(t *testing.T) {
	answer := writeAnswer(t, t.TempDir(), fixture.AnswerFile)
	for name, args := range map[string][]string{
		"no subcommand":      {"processor"},
		"other subcommand":   {"processor", "run", "img", "--answer", answer},
		"no image":           {"processor", "test", "--answer", answer},
		"no answer":          {"processor", "test", "img"},
		"two images":         {"processor", "test", "img", "other", "--answer", answer},
		"zero timeout":       {"processor", "test", "img", "--answer", answer, "--timeout", "0s"},
		"bad workspace id":   {"processor", "test", "img", "--answer", answer, "--workspace-id", "ws-1"},
		"unknown flag":       {"processor", "test", "img", "--answer", answer, "--frobnicate"},
		"flag without value": {"processor", "test", "img", "--answer"},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, _ := runCmd(t, args...); code != 2 {
				t.Errorf("exit code %d, want 2", code)
			}
		})
	}
}

// Input files are checked before any container runs.
func TestProcessorTestRejectsBadInput(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name   string
		answer string
		extra  []string
		stdout string
		stderr string
	}{
		{
			name:   "invalid answer",
			answer: "---\ntask: " + fixture.TaskA + "\ntask_version: 1.0.0\ntype: timestamp\nvalue: yesterday\n---\n",
			stdout: `format: value "yesterday" is not an RFC 3339 timestamp`,
			stderr: "1 problem(s) found in the answer or the previous output",
		},
		{
			name:   "answer without frontmatter",
			answer: "just text\n",
			stderr: `file must start with a "---" line`,
		},
		{
			name:   "task of another task",
			answer: fixture.AnswerFile,
			extra:  []string{"--task", "TASKB"},
			stderr: "but the answer is for task " + fixture.TaskA,
		},
		{
			name:   "task of another version",
			answer: fixture.AnswerFile,
			extra:  []string{"--task", "TASKA11"},
			stderr: "is version 1.1.0, but the answer is for version 1.0.0",
		},
		{
			name:   "attachment without blob",
			answer: "---\ntask: " + fixture.TaskA + "\ntask_version: 1.0.0\ntype: text\nvalue: done\nattachments:\n  - name: scan.pdf\n    sha256: " + fixture.SHA256 + "\n    media_type: application/pdf\n---\n",
			stderr: "there is no .custos/blobs/sha256 above",
		},
		{
			name:   "previous without output",
			answer: fixture.AnswerFile,
			extra:  []string{"--previous", "EMPTY"},
			stderr: "has neither generated/ nor documents/",
		},
		{
			name:   "invalid previous output",
			answer: fixture.AnswerFile,
			extra:  []string{"--previous", "BROKEN"},
			stdout: filepath.Join("BROKEN", "documents", fixture.DocID+".json") + ": format: match_key is missing",
			stderr: "1 problem(s) found",
		},
	}
	files := map[string]string{
		"TASKB":   filepath.Join(dir, "taskb.md"),
		"TASKA11": filepath.Join(dir, "taska11.md"),
		"EMPTY":   filepath.Join(dir, "empty"),
		"BROKEN":  filepath.Join(dir, "broken"),
	}
	fixture.WriteDir(t, dir, map[string]string{
		"taskb.md":          fixture.TaskFile(fixture.TaskB, "1.0.0"),
		"taska11.md":        fixture.TaskFile(fixture.TaskA, "1.1.0", fixture.TaskA+"@1.0.0"),
		"empty/custos.yaml": fixture.Config(fixture.WorkspaceID, fixture.Commit),
		"broken/documents/" + fixture.DocID + ".json": strings.Replace(fixture.DocumentFile, `"match_key":"slsa:web-01",`, "", 1),
	})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			answer := writeAnswer(t, t.TempDir(), c.answer)
			args := []string{"processor", "test", "custos.test/none:latest", "--answer", answer}
			for _, a := range c.extra {
				if f, ok := files[a]; ok {
					a = f
				}
				args = append(args, a)
			}
			code, out, errs := runCmd(t, args...)
			want := strings.ReplaceAll(c.stdout, "BROKEN", files["BROKEN"])
			if code != 1 || !strings.Contains(out, want) || !strings.Contains(errs, c.stderr) {
				t.Errorf("code %d\nstdout %q\nstderr %q", code, out, errs)
			}
		})
	}
}

func TestProcessorTestUnknownImage(t *testing.T) {
	answer := writeAnswer(t, t.TempDir(), fixture.AnswerFile)
	code, out, errs := runCmd(t, "processor", "test", "custos.test/does-not-exist:latest", "--answer", answer)
	if code != 1 || out != "" || !strings.Contains(errs, "custos processor test: ") {
		t.Errorf("code %d\nstdout %q\nstderr %q", code, out, errs)
	}
}

// previousOutput is a workspace checkout holding what the generate image
// produced earlier for the answer to TaskA: web-01 (unchanged), db-01 (gets
// a new title), old (no longer produced), the document slsa (unchanged)
// and the document gone (no longer produced). The checkout's custos.yaml
// and the answer of another task are ignored.
func previousOutput(t *testing.T) string {
	const (
		web  = "11111111-1111-4111-8111-111111111111"
		db   = "22222222-2222-4222-8222-222222222222"
		old  = "33333333-3333-4333-8333-333333333333"
		gone = "44444444-4444-4444-8444-444444444444"
	)
	producedBy := "produced_by:\n  task:\n    id: " + fixture.TaskA + "\n    version: 1.0.0\n  processor: test\n  digest: sha256:" + fixture.SHA256 + "\n  answer_commit: " + fixture.Commit + "\n"
	gen := func(id, key, title string) string {
		return "---\nid: " + id + "\nversion: 1.0.0\ntitle: " + title + "\nanswer_type: text\nmatch_key: " + key + "\n" + producedBy + "---\n\nGenerated for " + fixture.TaskA + "\n"
	}
	doc := func(key, content string) string {
		return `{"match_key":"` + key + `","name":"` + key + `","media_type":"application/vnd.in-toto+json",` +
			`"produced_by":{"task":{"id":"` + fixture.TaskA + `","version":"1.0.0"},"processor":"test","digest":"sha256:` + fixture.SHA256 +
			`","answer_commit":"` + fixture.Commit + `"},"verification":{"status":"unsigned"},"content":` + content + `}`
	}
	statement := `{"_type":"https://in-toto.io/Statement/v1","subject":[{"name":"web-01","digest":{"sha256":"` + sha256Hex("web-01") + `"}}],` +
		`"predicateType":"https://example.org/custos-test/v1","predicate":{}}`
	dir := t.TempDir()
	fixture.WriteDir(t, dir, map[string]string{
		"custos.yaml":                          fixture.Config(fixture.WorkspaceID, fixture.Commit),
		"answers/" + fixture.TaskB + ".md":     "not even an answer\n",
		"generated/" + web + "/1.0.0.md":       gen(web, "web-01", "Check web-01"),
		"generated/" + db + "/1.0.0.md":        gen(db, "db-01", "Check db-01"),
		"generated/" + old + "/1.0.0.md":       gen(old, "old", "Check old"),
		"documents/" + fixture.DocID + ".json": doc("slsa", statement),
		"documents/" + gone + ".json":          doc("gone", `{"_type":"https://in-toto.io/Statement/v1"}`),
	})
	return dir
}

func TestProcessorTestGolden(t *testing.T) {
	// A checkout with one attachment in .custos/blobs, found by walking up
	// from the answer file.
	checkout := t.TempDir()
	scan := "%PDF-1.7 scan\n"
	fixture.WriteDir(t, checkout, map[string]string{".custos/blobs/sha256/" + sha256Hex(scan): scan})
	withAttachment := "---\ntask: " + fixture.TaskA + "\ntask_version: 1.0.0\ntype: text\nvalue: done\nattachments:\n  - name: scan.pdf\n    sha256: " +
		sha256Hex(scan) + "\n    media_type: application/pdf\n---\n"
	secrets, publicKey := proctest.SigningKey(t)
	keys := t.TempDir()
	if err := os.WriteFile(filepath.Join(keys, "test.pem"), publicKey, 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		image  string
		answer string
		dir    string // where the answer file goes; "" = a new temp dir
		flags  []string
		code   int
		// contains replaces the golden file where the text comes from
		// contract.ParseOutput, whose messages are not fixed here.
		contains string
	}{
		{name: "echo", image: "echo", answer: withAttachment, dir: checkout},
		{name: "generate", image: "generate", answer: markdownAnswer(
			"task web-01 Check web-01\nchoice approve Approve the release\nprocessor web-01 host-scanner\norigin web-01 " + fixture.TaskB +
				"\ndoc slsa web-01\nstderr scanning 1 host\n")},
		{name: "generate-previous", image: "generate", answer: markdownAnswer(
			"task web-01 Check web-01\ntask db-01 Check db-01 again\nbump db-01 patch\ntask new-01 Check new-01\ndoc slsa web-01\n"),
			flags: []string{"--previous", "PREVIOUS"}},
		{name: "generate-nothing-changed", image: "generate", answer: markdownAnswer("doc slsa web-01\n"),
			flags: []string{"--previous", "PREVIOUS-SLSA"}},
		{name: "sign-verified", image: "sign", answer: markdownAnswer("doc slsa web-01\n"),
			flags: []string{"--secrets-dir", secrets, "--trusted-keys", keys}},
		{name: "sign-unchecked", image: "sign", answer: markdownAnswer("doc slsa web-01\n"),
			flags: []string{"--secrets-dir", secrets}},
		{name: "fail", image: "fail", answer: fixture.AnswerFile, code: 1},
		{name: "exit", image: "generate", answer: markdownAnswer("stderr giving up\nexit 4\n"), code: 1},
		{name: "garbage", image: "generate", answer: markdownAnswer("garbage\n"), code: 1, contains: "\n\nRun failed: invalid output: "},
		{name: "loop", image: "loop", answer: fixture.AnswerFile, flags: []string{"--timeout", "2s"}, code: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ref := proctest.Image(t, c.image)
			dir := c.dir
			if dir == "" {
				dir = t.TempDir()
			}
			args := []string{"processor", "test", ref, "--answer", writeAnswer(t, dir, c.answer)}
			for _, f := range c.flags {
				switch f {
				case "PREVIOUS":
					f = previousOutput(t)
				case "PREVIOUS-SLSA":
					f = previousOutput(t)
					for _, p := range []string{"generated", "documents/44444444-4444-4444-8444-444444444444.json"} {
						if err := os.RemoveAll(filepath.Join(f, p)); err != nil {
							t.Fatal(err)
						}
					}
				}
				args = append(args, f)
			}
			code, out, errs := runCmd(t, args...)
			if code != c.code || errs != "" {
				t.Fatalf("exit code %d (want %d), stderr %q\nstdout:\n%s", code, c.code, errs, out)
			}
			if c.contains != "" {
				if !strings.Contains(out, c.contains) {
					t.Errorf("stdout lacks %q:\n%s", c.contains, out)
				}
				return
			}
			golden(t, c.name, normalize(out, digestOf(ref)))
		})
	}
}
