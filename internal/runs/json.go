package runs

import (
	"github.com/emeland-io/custos/internal/match"
	"github.com/emeland-io/custos/internal/proposal"
	"github.com/emeland-io/custos/internal/workspace"
)

// ItemJSON is a match.Item in the REST API.
type ItemJSON struct {
	Kind         string                  `json:"kind"`
	MatchKey     string                  `json:"match_key"`
	Action       match.Action            `json:"action"`
	ID           string                  `json:"id"`
	From         string                  `json:"from,omitempty"`
	To           string                  `json:"to,omitempty"`
	Title        string                  `json:"title"`
	Verification *workspace.Verification `json:"verification,omitempty"`
}

func itemsJSON(items []match.Item) []ItemJSON {
	out := make([]ItemJSON, 0, len(items))
	for _, it := range items {
		out = append(out, ItemJSON{Kind: it.Kind, MatchKey: it.MatchKey, Action: it.Action, ID: it.ID,
			From: it.From, To: it.To, Title: it.Title, Verification: it.Verification})
	}
	return out
}

// ProposalJSON is a proposal.Proposal in the REST API.
type ProposalJSON struct {
	Branch  string     `json:"branch"`
	Commit  string     `json:"commit"`
	Task    string     `json:"task"`
	Digest  string     `json:"digest,omitempty"`
	Cascade bool       `json:"cascade"`
	Items   []ItemJSON `json:"items"`
}

func proposalJSON(p proposal.Proposal) ProposalJSON {
	return ProposalJSON{Branch: p.Branch, Commit: p.Commit, Task: p.Task, Digest: p.Digest, Cascade: p.Cascade, Items: itemsJSON(p.Items)}
}
