// Command hostlist is an example custos processor. It reads an answer that
// lists host names, one per line (Markdown list markers and # comments are
// allowed), and produces one follow-up task per host that asks when the
// host was last patched, plus an in-toto statement with the hosts as
// subjects.
//
// Build it as a static binary in an image of its own:
//
//	CGO_ENABLED=0 GOOS=linux go build -o hostlist ./examples/hostlist
//
//	FROM scratch
//	COPY hostlist /hostlist
//	ENTRYPOINT ["/hostlist"]
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	processor "github.com/emeland-io/custos/sdk/go"
)

func main() { processor.Main(process) }

var hostRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

func process(task processor.Task, answer processor.Answer) (processor.Output, error) {
	hosts, err := parseHosts(answer.Text())
	if err != nil {
		return processor.Output{}, err
	}
	var out processor.Output
	for _, h := range hosts {
		out.Tasks = append(out.Tasks, processor.OutputTask{
			MatchKey:   "host:" + h,
			Title:      "Patch level of " + h,
			Body:       "When was " + h + " last patched? Give the time of the last update.",
			AnswerType: processor.AnswerTimestamp,
		})
	}
	out.Documents = append(out.Documents, processor.OutputDocument{
		MatchKey:  "inventory",
		Name:      "Host inventory",
		MediaType: "application/vnd.in-toto+json",
		Content:   inventory(task, hosts),
	})
	return out, nil
}

// parseHosts returns the host names of the answer in the order they first
// appear, in lower case.
func parseHosts(text string) ([]string, error) {
	var hosts []string
	seen := map[string]bool{}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		h := strings.ToLower(line)
		if !hostRE.MatchString(h) {
			return nil, fmt.Errorf("line %d: %q is not a host name", i+1, line)
		}
		if !seen[h] {
			hosts = append(hosts, h)
			seen[h] = true
		}
	}
	if len(hosts) == 0 {
		return nil, errors.New("the answer lists no hosts; write one host name per line")
	}
	return hosts, nil
}

type statement struct {
	Type          string    `json:"_type"`
	Subject       []subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     predicate `json:"predicate"`
}

type subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type predicate struct {
	Task  string `json:"task"`
	Hosts int    `json:"hosts"`
}

// inventory is an unsigned in-toto statement. Hosts have no content to
// hash, so each subject's digest is the SHA-256 of its name.
func inventory(task processor.Task, hosts []string) statement {
	s := statement{
		Type:          "https://in-toto.io/Statement/v1",
		PredicateType: "https://example.org/custos/hostlist/v1",
		Predicate:     predicate{Task: task.ID + "@" + task.Version, Hosts: len(hosts)},
	}
	for _, h := range hosts {
		sum := sha256.Sum256([]byte(h))
		s.Subject = append(s.Subject, subject{Name: h, Digest: map[string]string{"sha256": hex.EncodeToString(sum[:])}})
	}
	return s
}
