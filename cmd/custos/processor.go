package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing/fstest"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"

	"github.com/emeland-io/custos/internal/attest"
	"github.com/emeland-io/custos/internal/attest/carabiner"
	"github.com/emeland-io/custos/internal/catalog"
	"github.com/emeland-io/custos/internal/contract"
	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/problem"
	"github.com/emeland-io/custos/internal/runner"
	"github.com/emeland-io/custos/internal/task"
	"github.com/emeland-io/custos/internal/workspace"
)

const processorUsage = `usage: custos processor test IMAGE --answer FILE [--task FILE] [--previous DIR] [--blobs DIR]
           [--secrets-dir DIR] [--network] [--timeout D] [--trusted-keys DIR]
           [--workspace-id UUID] [--container-runtime NAME]
`

const (
	// testWorkspaceID is the workspace id processor test sends when
	// --workspace-id is not given.
	testWorkspaceID = "00000000-0000-4000-8000-000000000000"
	// testProcessor is the registry name the tested image gets.
	testProcessor = "test"
	// testCommit stands for the workspace commit the answer was read from.
	testCommit = "0000000000000000000000000000000000000000"
	// testMaxDepth is serve's default for --max-generation-depth.
	testMaxDepth = 8
)

var secretNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func runProcessor(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "test" {
		fmt.Fprint(stderr, processorUsage)
		return 2
	}
	fl := flag.NewFlagSet("processor test", flag.ContinueOnError)
	fl.SetOutput(stderr)
	var pt processorTest
	fl.StringVar(&pt.answerPath, "answer", "", "answer file in the workspace format (answers/<task-uuid>.md)")
	fl.StringVar(&pt.taskPath, "task", "", "task version file (default: a task made up from the answer)")
	fl.StringVar(&pt.previousDir, "previous", "", "directory laid out like a workspace whose generated/ and documents/ hold earlier output")
	fl.StringVar(&pt.blobsDir, "blobs", "", "directory with the answer's attachments named by their SHA-256 (default: .custos/blobs/sha256 of the checkout holding the answer)")
	fl.StringVar(&pt.secretsDir, "secrets-dir", "", "every file in this directory is mounted as a secret at /run/secrets/<name>")
	fl.BoolVar(&pt.network, "network", false, "give the processor network access")
	fl.DurationVar(&pt.timeout, "timeout", 60*time.Second, "kill the processor after this time")
	fl.StringVar(&pt.trustedKeys, "trusted-keys", "", "directory with trusted public keys (*.pem, *.pub); without it signatures are not checked")
	fl.StringVar(&pt.workspaceID, "workspace-id", testWorkspaceID, "workspace id sent to the processor")
	fl.StringVar(&pt.runtime, "container-runtime", envOr("CUSTOS_CONTAINER_RUNTIME", "docker"), "docker, podman, or the path of either (env CUSTOS_CONTAINER_RUNTIME)")
	// The image may come before or after the flags.
	if err := fl.Parse(args[1:]); err != nil {
		return helpOrUsage(err)
	}
	if fl.NArg() > 0 {
		pt.image = fl.Arg(0)
		if err := fl.Parse(fl.Args()[1:]); err != nil {
			return helpOrUsage(err)
		}
	}
	if pt.image == "" || fl.NArg() != 0 || pt.answerPath == "" || pt.timeout <= 0 {
		fmt.Fprint(stderr, processorUsage)
		return 2
	}
	if !task.ValidID(pt.workspaceID) {
		fmt.Fprintf(stderr, "custos processor test: --workspace-id %q is not a lowercase UUID v4\n", pt.workspaceID)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return pt.run(ctx, stdout, stderr)
}

// processorTest runs one image on one answer the way serve would, and
// prints the proposal instead of writing it (spec §5.7).
type processorTest struct {
	image, answerPath, taskPath, previousDir, blobsDir string
	secretsDir, trustedKeys, workspaceID, runtime      string
	network                                            bool
	timeout                                            time.Duration
}

// testInput is everything processor test reads before it runs the image.
type testInput struct {
	ws      *workspace.Workspace
	version *task.Version
	answer  *workspace.Answer
	blobs   map[string]string
	secrets []string
}

func (pt processorTest) run(ctx context.Context, stdout, stderr io.Writer) int {
	in, err := pt.load(stdout)
	if err != nil {
		fmt.Fprintf(stderr, "custos processor test: %v\n", err)
		return 1
	}
	verifier := attest.Unverified
	if pt.trustedKeys != "" {
		v, err := carabiner.New(pt.trustedKeys)
		if err != nil {
			fmt.Fprintf(stderr, "custos processor test: --trusted-keys: %v\n", err)
			return 1
		}
		verifier = v
	}
	rn := runner.New(runner.Config{Runtime: pt.runtime, SecretsDir: pt.secretsDir, DefaultTimeout: pt.timeout})
	ref, digest, err := rn.Resolve(ctx, pt.image)
	if err != nil {
		fmt.Fprintf(stderr, "custos processor test: %v\n", err)
		return 1
	}
	input, err := json.Marshal(contract.NewInput(pt.workspaceID, in.version, in.answer))
	if err != nil {
		fmt.Fprintf(stderr, "custos processor test: %v\n", err)
		return 1
	}
	res, err := rn.Run(ctx, runner.Job{Image: ref, Timeout: pt.timeout, Network: pt.network, Secrets: in.secrets, Blobs: in.blobs, Input: input})
	if err != nil {
		fmt.Fprintf(stderr, "custos processor test: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Image:  %s\nDigest: %s\nTask:   %s@%s\n\n", pt.image, digest, in.version.ID, in.version.Version)
	ch, assumed, failure := pt.plan(in, res, digest, verifier)
	if failure != "" {
		fmt.Fprintf(stdout, "Run failed: %s\n", failure)
	} else {
		printChanges(stdout, ch, assumed)
	}
	printLog(stdout, res.Log)
	if failure != "" {
		return 1
	}
	return 0
}

// plan checks the run's result and matches its output against the
// previous output. failure is empty when the run succeeded.
func (pt processorTest) plan(in *testInput, res *runner.Result, digest string, v attest.Verifier) (ch *match.Changes, assumed []string, failure string) {
	switch {
	case res.TimedOut:
		return nil, nil, fmt.Sprintf("the processor did not finish within %s", pt.timeout)
	case res.ExitCode != 0:
		return nil, nil, fmt.Sprintf("the processor exited with status %d", res.ExitCode)
	}
	out, err := contract.ParseOutput(res.Stdout)
	if err != nil {
		return nil, nil, fmt.Sprintf("invalid output: %v", err)
	}
	cat, assumed, err := standInCatalog(in, out, digest)
	if err != nil {
		return nil, nil, err.Error()
	}
	ch, err = match.Plan(match.Run{
		Workspace:    in.ws,
		Catalog:      cat,
		Task:         in.version.Ref(),
		Processor:    testProcessor,
		Digest:       digest,
		AnswerCommit: testCommit,
		MaxDepth:     testMaxDepth,
		Verifier:     v,
		NewID:        uuid.NewString,
	}, out)
	if err != nil {
		return nil, nil, fmt.Sprintf("matching the output: %v", err)
	}
	return ch, assumed, ""
}

// load reads the answer, the task, the previous output, the attachments
// and the secret names. Problems in the files are printed to stdout, as
// custos validate does, and make load fail.
func (pt processorTest) load(stdout io.Writer) (*testInput, error) {
	data, err := os.ReadFile(pt.answerPath)
	if err != nil {
		return nil, err
	}
	var head workspace.Answer
	if _, err := frontmatter.Decode(data, &head); err != nil {
		return nil, fmt.Errorf("%s: %v", pt.answerPath, err)
	}
	if !task.ValidID(head.Task) {
		return nil, fmt.Errorf("%s: task %q is not a lowercase UUID v4", pt.answerPath, head.Task)
	}
	answerPath := "answers/" + head.Task + ".md"
	config, err := workspace.MarshalConfig(workspace.Config{Workspace: pt.workspaceID, Catalog: workspace.CatalogPin{URL: "processor-test", Commit: testCommit}})
	if err != nil {
		return nil, err
	}
	tree := fstest.MapFS{
		workspace.ConfigPath: {Data: config},
		answerPath:           {Data: data},
	}
	display := map[string]string{answerPath: pt.answerPath}
	if pt.previousDir != "" {
		if err := addPrevious(tree, display, pt.previousDir); err != nil {
			return nil, err
		}
	}
	ws, ps := workspace.Load(tree)
	ps = append(ps, ws.Graph.Check()...)
	if len(ps) > 0 {
		problem.Sort(ps)
		for _, p := range ps {
			if d, ok := display[p.Path]; ok {
				p.Path = d
			}
			fmt.Fprintln(stdout, p)
		}
		return nil, fmt.Errorf("%d problem(s) found in the answer or the previous output", len(ps))
	}
	in := &testInput{ws: ws, answer: ws.Answers[answerPath]}
	if in.version, err = pt.taskVersion(in.answer); err != nil {
		return nil, err
	}
	if in.blobs, err = pt.attachments(in.answer); err != nil {
		return nil, err
	}
	if in.secrets, err = secretNames(pt.secretsDir); err != nil {
		return nil, err
	}
	return in, nil
}

// addPrevious copies generated/ and documents/ of dir into tree. Other
// files (custos.yaml, answers) are ignored, so dir may be a workspace
// checkout.
func addPrevious(tree fstest.MapFS, display map[string]string, dir string) error {
	found := false
	for _, sub := range []string{"generated", "documents"} {
		root := filepath.Join(dir, sub)
		if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		found = true
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(dir, p)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(rel)
			tree[key] = &fstest.MapFile{Data: data}
			display[key] = p
			return nil
		})
		if err != nil {
			return fmt.Errorf("--previous: %v", err)
		}
	}
	if !found {
		return fmt.Errorf("--previous %s has neither generated/ nor documents/", dir)
	}
	return nil
}

// taskVersion reads --task, or makes up the task from the answer.
func (pt processorTest) taskVersion(a *workspace.Answer) (*task.Version, error) {
	if pt.taskPath == "" {
		m := task.Meta{ID: a.Task, Version: a.TaskVersion, Title: "Test task", AnswerType: a.Type}
		if a.Type == task.AnswerChoice {
			m.Choices = []string{a.Value}
		}
		return &task.Version{Meta: m, Path: "tasks/" + m.ID + "/" + m.Version + ".md"}, nil
	}
	data, err := os.ReadFile(pt.taskPath)
	if err != nil {
		return nil, err
	}
	var m task.Meta
	body, err := frontmatter.Decode(data, &m)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", pt.taskPath, err)
	}
	p := "tasks/" + m.ID + "/" + m.Version + ".md"
	if ps := task.CheckMeta(m, p, "tasks"); len(ps) > 0 {
		return nil, fmt.Errorf("%s: %s", pt.taskPath, ps[0].Message)
	}
	switch {
	case m.ID != a.Task:
		return nil, fmt.Errorf("%s is task %s, but the answer is for task %s", pt.taskPath, m.ID, a.Task)
	case m.Version != a.TaskVersion:
		return nil, fmt.Errorf("%s is version %s, but the answer is for version %s", pt.taskPath, m.Version, a.TaskVersion)
	case m.AnswerType != a.Type:
		return nil, fmt.Errorf("%s asks for answer type %s, but the answer has type %s", pt.taskPath, m.AnswerType, a.Type)
	case m.AnswerType == task.AnswerChoice && !slices.Contains(m.Choices, a.Value):
		return nil, fmt.Errorf("the answer %q is not one of the choices of %s", a.Value, pt.taskPath)
	}
	return &task.Version{Meta: m, Body: body, Path: p}, nil
}

// attachments finds the file of every attachment and checks its hash.
func (pt processorTest) attachments(a *workspace.Answer) (map[string]string, error) {
	blobs := map[string]string{}
	if len(a.Attachments) == 0 {
		return blobs, nil
	}
	dir := pt.blobsDir
	if dir == "" {
		dir = findCheckoutBlobs(pt.answerPath)
		if dir == "" {
			return nil, fmt.Errorf("the answer has attachments, but there is no .custos/blobs/sha256 above %s; give --blobs", pt.answerPath)
		}
	}
	for _, at := range a.Attachments {
		p, err := filepath.Abs(filepath.Join(dir, at.SHA256))
		if err != nil {
			return nil, err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("attachment %s: %v", at.Name, err)
		}
		if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != at.SHA256 {
			return nil, fmt.Errorf("attachment %s: %s does not match its SHA-256", at.Name, p)
		}
		blobs[at.SHA256] = p
	}
	return blobs, nil
}

// findCheckoutBlobs returns .custos/blobs/sha256 of the nearest directory
// above the answer file that has one, or "".
func findCheckoutBlobs(answerPath string) string {
	dir, err := filepath.Abs(filepath.Dir(answerPath))
	if err != nil {
		return ""
	}
	for {
		p := filepath.Join(dir, ".custos", "blobs", "sha256")
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// secretNames lists the files of dir that can be secret names.
func secretNames(dir string) ([]string, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("--secrets-dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && secretNameRE.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

// standInCatalog builds the catalog match.Plan checks the output against.
// Without the real catalog, processor names and origins the output uses
// are assumed to exist; the returned lines say which.
func standInCatalog(in *testInput, out *contract.Output, digest string) (*catalog.Catalog, []string, error) {
	files := fstest.MapFS{}
	addTask := func(m task.Meta, body string) error {
		data, err := frontmatter.Encode(m, body)
		if err != nil {
			return err
		}
		files[path.Join("tasks", m.ID, m.Version+".md")] = &fstest.MapFile{Data: data}
		return nil
	}
	if err := addTask(in.version.Meta, in.version.Body); err != nil {
		return nil, nil, err
	}
	image := "processor-test@" + digest
	reg := catalog.Registry{
		Processors: map[string]catalog.Processor{testProcessor: {Image: image}},
		Bindings:   map[string]string{in.version.ID: testProcessor},
	}
	var assumed []string
	origins := map[string]string{}
	for _, t := range out.Tasks {
		if t.Processor != "" {
			if _, ok := reg.Processors[t.Processor]; !ok {
				reg.Processors[t.Processor] = catalog.Processor{Image: image}
				assumed = append(assumed, "processor "+t.Processor+" is registered")
			}
		}
		if t.Origin != nil && t.Origin.ID != in.version.ID && !in.ws.Graph.Has(t.Origin.ID) {
			if _, ok := origins[t.Origin.ID]; !ok {
				origins[t.Origin.ID] = t.Origin.Version
				assumed = append(assumed, "task "+t.Origin.ID+" exists (origin of "+t.MatchKey+")")
			}
		}
	}
	for _, id := range slices.Sorted(maps.Keys(origins)) {
		v := origins[id]
		if v == "" {
			v = "1.0.0"
		}
		if err := addTask(task.Meta{ID: id, Version: v, Title: "Origin", AnswerType: task.AnswerMarkdown}, ""); err != nil {
			return nil, nil, err
		}
	}
	data, err := yaml.Marshal(reg)
	if err != nil {
		return nil, nil, err
	}
	files["processors.yaml"] = &fstest.MapFile{Data: data}
	cat, _ := catalog.Load(files)
	slices.Sort(assumed)
	return cat, assumed, nil
}

func printChanges(w io.Writer, ch *match.Changes, assumed []string) {
	counts := map[match.Action]int{}
	for _, it := range ch.Items {
		counts[it.Action]++
	}
	if len(ch.Files) == 0 {
		fmt.Fprintln(w, "No proposal: nothing would change.")
	} else {
		fmt.Fprintf(w, "Proposal: %d added, %d new version, %d removed, %d unchanged\n",
			counts[match.Added], counts[match.NewVersion], counts[match.Removed], counts[match.Unchanged])
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, it := range ch.Items {
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", it.Action, it.Kind, it.MatchKey, versions(it), itemTitle(it))
	}
	tw.Flush()
	if len(assumed) > 0 {
		fmt.Fprintln(w, "\nAssumed without the catalog:")
		for _, a := range assumed {
			fmt.Fprintf(w, "  %s\n", a)
		}
	}
}

func versions(it match.Item) string {
	switch {
	case it.From != "" && it.To != "" && it.From != it.To:
		return it.From + " -> " + it.To
	case it.To != "":
		return it.To
	default:
		return it.From
	}
}

func itemTitle(it match.Item) string {
	if it.Verification == nil {
		return it.Title
	}
	if len(it.Verification.Signers) == 0 {
		return it.Title + " (" + it.Verification.Status + ")"
	}
	return it.Title + " (" + it.Verification.Status + ": " + strings.Join(it.Verification.Signers, ", ") + ")"
}

func printLog(w io.Writer, log []byte) {
	if len(log) == 0 {
		fmt.Fprintln(w, "\nLog: (empty)")
		return
	}
	fmt.Fprintf(w, "\nLog:\n%s", log)
	if log[len(log)-1] != '\n' {
		fmt.Fprintln(w)
	}
}
