import os
import subprocess
import sys
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
SDK = os.path.dirname(HERE)
SCRIPT = os.path.join(SDK, "examples", "hostlist.py")

INPUT = (
    '{"contract": "custos.processor/v1", "workspace": {"id": "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"},'
    ' "task": {"id": "3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f", "version": "1.0.0", "title": "Hosts", "body": "", "answer_type": "markdown"},'
    ' "answer": {"task_version": "1.0.0", "value": null, "body": "# production\\n- Web-01\\n- db-01\\n\\nweb-01\\n", "attachments": []}}'
)

# The same bytes as the Go example produces (sdk/go/examples/hostlist/main_test.go).
WANT = (
    '{"tasks":['
    '{"match_key":"host:web-01","title":"Patch level of web-01","body":"When was web-01 last patched? Give the time of the last update.","answer_type":"timestamp"},'
    '{"match_key":"host:db-01","title":"Patch level of db-01","body":"When was db-01 last patched? Give the time of the last update.","answer_type":"timestamp"}],'
    '"documents":[{"match_key":"inventory","name":"Host inventory","media_type":"application/vnd.in-toto+json","content":'
    '{"_type":"https://in-toto.io/Statement/v1","subject":['
    '{"name":"web-01","digest":{"sha256":"0b9d9f06cc8cf44765a12c0aa01a6e2217130979f4dfeda24e22b0af7e8d237e"}},'
    '{"name":"db-01","digest":{"sha256":"82cf7f0efd2059fab88ef0bd227131f5e90ffd5e383fc038878a17fcdeb9e28e"}}],'
    '"predicateType":"https://example.org/custos/hostlist/v1","predicate":{"task":"3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f@1.0.0","hosts":2}}}]}\n'
)


def run_hostlist(stdin):
    env = dict(os.environ, PYTHONPATH=SDK)
    return subprocess.run([sys.executable, SCRIPT], input=stdin.encode("utf-8"), capture_output=True, env=env, timeout=60)


class HostlistTest(unittest.TestCase):
    def test_output(self):
        p = run_hostlist(INPUT)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(p.stdout.decode("utf-8"), WANT)

    def test_empty_answer(self):
        p = run_hostlist(INPUT.replace("# production\\n- Web-01\\n- db-01\\n\\nweb-01\\n", "# nothing yet\\n"))
        self.assertEqual(p.returncode, 1)
        self.assertEqual(p.stdout, b"")
        self.assertEqual(p.stderr.decode(), "the answer lists no hosts; write one host name per line\n")

    def test_bad_host_name(self):
        p = run_hostlist(INPUT.replace("- db-01", "- db 01"))
        self.assertEqual(p.returncode, 1)
        self.assertEqual(p.stderr.decode(), "line 3: 'db 01' is not a host name\n")


if __name__ == "__main__":
    unittest.main()
