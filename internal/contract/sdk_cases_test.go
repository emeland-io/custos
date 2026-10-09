package contract

import (
	"encoding/json"
	"os"
	"testing"
)

// TestSDKOutputCases checks ParseOutput against sdk/testdata/output-cases.json,
// the cases both SDKs are tested with, so the server and the SDKs agree on
// what a valid output is.
func TestSDKOutputCases(t *testing.T) {
	data, err := os.ReadFile("../../sdk/testdata/output-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name   string `json:"name"`
		Valid  bool   `json:"valid"`
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, err := ParseOutput([]byte(c.Stdout))
			if c.Valid && err != nil {
				t.Errorf("want valid, got %v", err)
			}
			if !c.Valid && err == nil {
				t.Error("want invalid, got valid")
			}
		})
	}
}
