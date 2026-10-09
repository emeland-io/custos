package main

import (
	"bytes"
	"strings"
	"testing"

	processor "github.com/emeland-io/custos/sdk/go"
)

const input = `{"contract": "custos.processor/v1", "workspace": {"id": "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"},
 "task": {"id": "3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f", "version": "1.0.0", "title": "Hosts", "body": "", "answer_type": "markdown"},
 "answer": {"task_version": "1.0.0", "value": null, "body": "# production\n- Web-01\n- db-01\n\nweb-01\n", "attachments": []}}`

func TestHostlist(t *testing.T) {
	var out bytes.Buffer
	if err := processor.Run(process, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	want := `{"tasks":[` +
		`{"match_key":"host:web-01","title":"Patch level of web-01","body":"When was web-01 last patched? Give the time of the last update.","answer_type":"timestamp"},` +
		`{"match_key":"host:db-01","title":"Patch level of db-01","body":"When was db-01 last patched? Give the time of the last update.","answer_type":"timestamp"}],` +
		`"documents":[{"match_key":"inventory","name":"Host inventory","media_type":"application/vnd.in-toto+json","content":` +
		`{"_type":"https://in-toto.io/Statement/v1","subject":[` +
		`{"name":"web-01","digest":{"sha256":"0b9d9f06cc8cf44765a12c0aa01a6e2217130979f4dfeda24e22b0af7e8d237e"}},` +
		`{"name":"db-01","digest":{"sha256":"82cf7f0efd2059fab88ef0bd227131f5e90ffd5e383fc038878a17fcdeb9e28e"}}],` +
		`"predicateType":"https://example.org/custos/hostlist/v1","predicate":{"task":"3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f@1.0.0","hosts":2}}}]}` + "\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestHostlistRejectsEmptyAnswer(t *testing.T) {
	in := strings.Replace(input, `# production\n- Web-01\n- db-01\n\nweb-01\n`, `# nothing yet\n`, 1)
	err := processor.Run(process, strings.NewReader(in), &bytes.Buffer{})
	if err == nil || err.Error() != "the answer lists no hosts; write one host name per line" {
		t.Errorf("err %v", err)
	}
}

func TestHostlistRejectsBadHostName(t *testing.T) {
	in := strings.Replace(input, `- db-01`, `- db 01`, 1)
	err := processor.Run(process, strings.NewReader(in), &bytes.Buffer{})
	if err == nil || err.Error() != `line 3: "db 01" is not a host name` {
		t.Errorf("err %v", err)
	}
}
