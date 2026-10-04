// Command generate is the test processor that turns the directives in the
// answer text into output tasks and bare in-toto statements (see package
// directives and the architecture note of phase 3).
package main

import (
	"encoding/json"

	"github.com/emeland-io/custos/internal/proctest/images/directives"
)

func main() {
	directives.Main("generate", func(statement []byte) (json.RawMessage, error) {
		return statement, nil
	})
}
