package auth

import (
	"testing"

	"github.com/emeland-io/custos/internal/gitrepo"
)

const (
	ws1 = "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"
	ws2 = "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d"
)

func TestRolesHas(t *testing.T) {
	for _, tc := range []struct {
		roles    Roles
		role, ws string
		want     bool
	}{
		{Roles{}, RoleAdmin, "", false},
		{Roles{}, RoleEngineer, ws1, false},
		{Roles{}, RoleEngineer, "", false},
		{Roles{Admin: true}, RoleAdmin, "", true},
		{Roles{Admin: true}, RoleTaskAuthor, "", true},
		{Roles{Admin: true}, RoleProcessorAuthor, "", true},
		{Roles{Admin: true}, RoleEngineer, ws1, true},
		{Roles{TaskAuthor: true}, RoleTaskAuthor, "", true},
		{Roles{TaskAuthor: true}, RoleProcessorAuthor, "", false},
		{Roles{TaskAuthor: true}, RoleAdmin, "", false},
		{Roles{ProcessorAuthor: true}, RoleProcessorAuthor, "", true},
		{Roles{ProcessorAuthor: true}, RoleTaskAuthor, "", false},
		{Roles{Engineer: []string{ws1}}, RoleEngineer, ws1, true},
		{Roles{Engineer: []string{ws1}}, RoleEngineer, ws2, false},
		{Roles{Engineer: []string{ws1}}, RoleEngineer, "", true}, // engineer of some workspace
		{Roles{Engineer: []string{ws1}}, RoleTaskAuthor, "", false},
		{Roles{Engineer: []string{AllWorkspaces}}, RoleEngineer, ws2, true},
		{Roles{Engineer: []string{ws1}}, "engineers", ws1, false}, // unknown role
	} {
		if got := tc.roles.Has(tc.role, tc.ws); got != tc.want {
			t.Errorf("%+v.Has(%q, %q) = %v, want %v", tc.roles, tc.role, tc.ws, got, tc.want)
		}
	}
}

func TestPrincipal(t *testing.T) {
	p := Principal{Subject: "s", Name: "Jane Doe", Email: "jane@example.org", Roles: Roles{Engineer: []string{ws1}}}
	if p.Signature() != (gitrepo.Signature{Name: "Jane Doe", Email: "jane@example.org"}) {
		t.Errorf("signature %+v", p.Signature())
	}
	if !p.Has(RoleEngineer, ws1) || p.Has(RoleEngineer, ws2) {
		t.Error("Principal.Has does not follow Roles.Has")
	}
}
