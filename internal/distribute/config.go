// Package distribute brings workspaces in line with the catalog (spec §4.1).
// When the catalog's main advances, an unfrozen workspace gets a commit by
// custos-bot that moves its pin; a frozen workspace gets one pin proposal
// branch custos/pin/<catalog-commit>, which an engineer accepts or rejects.
package distribute

import (
	"fmt"
	"io/fs"
	"reflect"

	"github.com/emeland-io/custos/internal/frontmatter"
	"github.com/emeland-io/custos/internal/gitrepo"
	"github.com/emeland-io/custos/internal/store"
	"github.com/emeland-io/custos/internal/workspace"
)

const configPath = "custos.yaml"

// errNoMain reports that workspace id has no main branch yet (for example
// still being created by a fork), consistently across every entry point
// that needs main to exist.
func errNoMain(id string) error {
	return fmt.Errorf("workspace %s has no main branch: %w", id, store.ErrNotFound)
}

// decodeConfig reads custos.yaml strictly: an unknown field is an error, so
// rewriting the file can never drop a field custos does not know.
func decodeConfig(data []byte) (workspace.Config, error) {
	var c workspace.Config
	if err := frontmatter.DecodeStrict(data, &c); err != nil {
		return c, fmt.Errorf("%s: %w", configPath, err)
	}
	if c.Catalog.Commit == "" {
		return c, fmt.Errorf("%s: catalog.commit is missing", configPath)
	}
	return c, nil
}

// setConfig applies change to the custos.yaml in data. changed is false, and
// out nil, when change left every field as it was.
func setConfig(data []byte, change func(*workspace.Config)) (out []byte, changed bool, err error) {
	c, err := decodeConfig(data)
	if err != nil {
		return nil, false, err
	}
	before := c
	change(&c)
	if reflect.DeepEqual(c, before) {
		return nil, false, nil
	}
	if out, err = workspace.MarshalConfig(c); err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// editConfig is setConfig for the tree of a store.UpdateWorkspace edit. It
// returns no changes when change left custos.yaml as it was.
func editConfig(tree fs.FS, change func(*workspace.Config)) ([]gitrepo.Change, error) {
	data, err := fs.ReadFile(tree, configPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", configPath, err)
	}
	out, changed, err := setConfig(data, change)
	if err != nil || !changed {
		return nil, err
	}
	return []gitrepo.Change{{Path: configPath, Data: out}}, nil
}

// readConfig reads custos.yaml at revision rev of a workspace repository.
func readConfig(repo *gitrepo.Repo, rev string) (workspace.Config, error) {
	data, ok, err := repo.ReadFile(rev, configPath)
	if err != nil {
		return workspace.Config{}, err
	}
	if !ok {
		return workspace.Config{}, fmt.Errorf("%s is missing at %s", configPath, rev)
	}
	return decodeConfig(data)
}
