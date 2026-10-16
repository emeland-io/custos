package main

import "os"

// CUSTOS_TEST_LEGACY_CLI=1 runs the whole package's tests against the
// pre-cobra dispatcher (ruling 5.8); `make test` does both.
func init() { legacyCLI = os.Getenv("CUSTOS_TEST_LEGACY_CLI") != "" }
