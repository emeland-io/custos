// Command fail is the test processor that always fails: it writes "boom" to
// stderr and exits with code 3.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	_, _ = io.Copy(io.Discard, os.Stdin)
	fmt.Fprintln(os.Stderr, "boom")
	os.Exit(3)
}
