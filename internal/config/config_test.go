package config

import (
	"path/filepath"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
		want Config
	}{
		{
			name: "flags",
			args: []string{"--root-dir", "r", "--work-dir", "w", "--keys-dir", "k", "--addr", ":1", "--sigstore-online"},
			want: Config{RootDir: "r", WorkDir: "w", KeysDir: "k", Addr: ":1", SigstoreOnline: true},
		},
		{
			name: "env",
			env: map[string]string{
				"CUSTOS_ROOT_DIR": "r", "CUSTOS_WORK_DIR": "w", "CUSTOS_KEYS_DIR": "k",
				"CUSTOS_ADDR": ":2", "CUSTOS_SIGSTORE_ONLINE": "true",
			},
			want: Config{RootDir: "r", WorkDir: "w", KeysDir: "k", Addr: ":2", SigstoreOnline: true},
		},
		{
			name: "flag wins over env",
			args: []string{"--root-dir", "flag", "--sigstore-online=false"},
			env:  map[string]string{"CUSTOS_ROOT_DIR": "env", "CUSTOS_WORK_DIR": "w", "CUSTOS_SIGSTORE_ONLINE": "1"},
			want: Config{RootDir: "flag", WorkDir: "w", KeysDir: filepath.Join("w", "keys"), Addr: ":8080"},
		},
		{
			name: "defaults",
			args: []string{"--root-dir", "r", "--work-dir", "w"},
			want: Config{RootDir: "r", WorkDir: "w", KeysDir: filepath.Join("w", "keys"), Addr: ":8080"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.args, env(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		env  map[string]string
	}{
		"missing root dir": {args: []string{"--work-dir", "w"}},
		"missing work dir": {args: []string{"--root-dir", "r"}},
		"bad bool env":     {args: []string{"--root-dir", "r", "--work-dir", "w"}, env: map[string]string{"CUSTOS_SIGSTORE_ONLINE": "maybe"}},
	} {
		if _, err := Parse(tc.args, env(tc.env)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
