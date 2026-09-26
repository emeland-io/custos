package main

import (
	"strings"
	"testing"

	"github.com/emeland-io/custos/internal/config"
)

func TestWriteBanner(t *testing.T) {
	var b strings.Builder
	writeBanner(&b, config.Config{RootDir: "/data/root", WorkDir: "/data/work", Addr: ":9000"})
	out := b.String()
	for _, want := range []string{
		"listens on port 9000",
		"-p 9090:9000",
		"-v custos-root:/data/root",
		"-v custos-work:/data/work",
		"http://localhost:9090",
		"--no-banner",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner lacks %q:\n%s", want, out)
		}
	}
}
