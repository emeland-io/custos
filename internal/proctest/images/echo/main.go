// Command echo is the test processor that returns its input: no tasks and
// one document "input" whose content is the input JSON object.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, in); err != nil {
		fail(fmt.Errorf("input is not JSON: %w", err))
	}
	out := map[string]any{
		"tasks": []any{},
		"documents": []any{map[string]any{
			"match_key":  "input",
			"name":       "input",
			"media_type": "application/json",
			"content":    json.RawMessage(compact.Bytes()),
		}},
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "echo:", err)
	os.Exit(1)
}
