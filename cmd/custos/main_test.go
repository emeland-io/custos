package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"help"}, nil, &out, &errb); code != 0 {
		t.Fatalf("exit code %d, stderr %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "custos validate") {
		t.Errorf("usage does not mention validate:\n%s", out.String())
	}
}

func TestNoArguments(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run(nil, nil, &out, &errb); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"frobnicate"}, nil, &out, &errb); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if !strings.Contains(errb.String(), `unknown command "frobnicate"`) {
		t.Errorf("stderr: %s", errb.String())
	}
}
