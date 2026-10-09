import io
import json
import os
import subprocess
import sys
import unittest

from custos_processor import (
    MAX_OUTPUT_SIZE,
    ContractError,
    Origin,
    Output,
    OutputDocument,
    OutputTask,
    encode,
    process_stream,
    read_input,
    validate,
)

HERE = os.path.dirname(os.path.abspath(__file__))
SDK = os.path.dirname(HERE)

SAMPLE_INPUT = json.dumps(
    {
        "contract": "custos.processor/v1",
        "workspace": {"id": "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9"},
        "task": {
            "id": "3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f",
            "version": "1.1.0",
            "title": "Hosts",
            "body": "List the hosts.",
            "answer_type": "markdown",
        },
        "answer": {
            "task_version": "1.1.0",
            "value": None,
            "body": "web-01\n",
            "attachments": [
                {
                    "name": "scan.pdf",
                    "sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
                    "media_type": "application/pdf",
                    "path": "/input/blobs/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
                }
            ],
        },
        "added_in_a_later_version": True,
    }
)


def output_from_dict(d):
    """Builds an Output from decoded stdout, the way a processor would."""
    tasks = []
    for t in d.get("tasks") or []:
        origin = t.get("origin")
        tasks.append(
            OutputTask(
                match_key=t.get("match_key"),
                title=t.get("title"),
                answer_type=t.get("answer_type"),
                body=t.get("body", ""),
                choices=t.get("choices") or [],
                origin=Origin(id=origin.get("id"), version=origin.get("version")) if origin is not None else None,
                processor=t.get("processor"),
                bump=t.get("bump"),
            )
        )
    docs = [
        OutputDocument(match_key=x.get("match_key"), name=x.get("name"), media_type=x.get("media_type"), content=x.get("content"))
        for x in d.get("documents") or []
    ]
    return Output(tasks=tasks, documents=docs)


class ReadInputTest(unittest.TestCase):
    def test_task_and_answer(self):
        inp = read_input(io.StringIO(SAMPLE_INPUT))
        self.assertEqual(inp.workspace_id, "5b6c7d8e-9f0a-4b1c-a2d3-e4f5a6b7c8d9")
        self.assertEqual(inp.task.id, "3f1c2a4e-8b7d-4c1a-9e2f-1a2b3c4d5e6f")
        self.assertEqual(inp.task.answer_type, "markdown")
        self.assertIsNone(inp.answer.value)
        self.assertEqual(inp.answer.text, "web-01\n")
        self.assertEqual(inp.answer.attachments[0].path, "/input/blobs/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")

    def test_text_prefers_value(self):
        doc = json.loads(SAMPLE_INPUT)
        doc["answer"]["value"] = "2026-10-04T12:00:00Z"
        inp = read_input(io.StringIO(json.dumps(doc)))
        self.assertEqual(inp.answer.text, "2026-10-04T12:00:00Z")

    def test_other_contract(self):
        with self.assertRaisesRegex(ContractError, 'contract "custos.processor/v2"'):
            read_input(io.StringIO(SAMPLE_INPUT.replace("custos.processor/v1", "custos.processor/v2")))

    def test_broken_input(self):
        for text in ("not json", SAMPLE_INPUT + " {}", "", "[]"):
            with self.subTest(text=text[:10]):
                with self.assertRaisesRegex(ContractError, "input"):
                    read_input(io.StringIO(text))


class EncodeTest(unittest.TestCase):
    def test_omits_empty_optional_fields(self):
        data = encode(Output(tasks=[OutputTask(match_key="host:web-01", title="Patch web-01", answer_type="timestamp")]))
        self.assertEqual(
            data,
            b'{"tasks":[{"match_key":"host:web-01","title":"Patch web-01","body":"","answer_type":"timestamp"}],"documents":[]}\n',
        )

    def test_every_field(self):
        task = OutputTask(
            match_key="a",
            title="A",
            answer_type="choice",
            body="b",
            choices=["yes", "no"],
            origin=Origin(id="9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f", version="1.2.0"),
            processor="host-scanner",
            bump="major",
        )
        doc = OutputDocument(match_key="d", name="n", media_type="application/json", content={"<": "&", "ü": 1})
        self.assertEqual(
            encode(Output(tasks=[task], documents=[doc])).decode("utf-8"),
            '{"tasks":[{"match_key":"a","title":"A","body":"b","answer_type":"choice","choices":["yes","no"],'
            '"origin":{"id":"9f8e7d6c-5b4a-4c3d-8e2f-1a0b9c8d7e6f","version":"1.2.0"},"processor":"host-scanner","bump":"major"}],'
            '"documents":[{"match_key":"d","name":"n","media_type":"application/json","content":{"<":"&","ü":1}}]}\n',
        )

    def test_reports_every_problem(self):
        out = Output(
            tasks=[
                OutputTask(match_key="a", title="A", answer_type="number"),
                OutputTask(match_key="a", title="", answer_type="text", bump="huge"),
            ],
            documents=[OutputDocument(match_key="d", name="n", media_type="", content=None)],
        )
        self.assertEqual(
            validate(out),
            [
                'tasks[0]: answer_type "number" is not one of markdown, text, timestamp, path, url, choice',
                'tasks[1]: match_key "a" is used by an earlier task',
                "tasks[1]: title is missing",
                'tasks[1]: bump "huge" is not one of patch, minor, major',
                "documents[0]: media_type is missing",
                "documents[0]: content is missing",
            ],
        )
        with self.assertRaises(ContractError) as cm:
            encode(out)
        self.assertEqual(len(cm.exception.problems), 6)

    def test_wrong_return_type(self):
        self.assertEqual(validate({"tasks": []}), ["process must return an Output, not dict"])

    def test_content_not_json(self):
        out = Output(documents=[OutputDocument(match_key="d", name="n", media_type="x", content={1, 2})])
        self.assertRegex(validate(out)[0], r"^documents\[0\]: content cannot be encoded as JSON")

    def test_oversized(self):
        out = Output(documents=[OutputDocument(match_key="d", name="n", media_type="text/plain", content="x" * MAX_OUTPUT_SIZE)])
        with self.assertRaisesRegex(ContractError, "custos accepts at most 16777216"):
            encode(out)


class SharedCasesTest(unittest.TestCase):
    """sdk/testdata/output-cases.json is checked by the server's parser and
    by both SDKs, so they agree on what a valid output is."""

    def test_cases(self):
        with open(os.path.join(SDK, "..", "testdata", "output-cases.json"), encoding="utf-8") as f:
            cases = json.load(f)
        self.assertTrue(cases)
        for case in cases:
            if case.get("parser_only"):
                continue  # malformed JSON cannot be built as an Output value
            with self.subTest(case=case["name"]):
                problems = validate(output_from_dict(json.loads(case["stdout"])))
                if case["valid"]:
                    self.assertEqual(problems, [])
                else:
                    self.assertNotEqual(problems, [])


class ProcessStreamTest(unittest.TestCase):
    def test_writes_nothing_on_error(self):
        out = io.BytesIO()

        def process(task, answer):
            raise ValueError("no hosts found")

        with self.assertRaisesRegex(ValueError, "no hosts found"):
            process_stream(process, io.StringIO(SAMPLE_INPUT), out)
        self.assertEqual(out.getvalue(), b"")


def run_script(script, stdin):
    env = dict(os.environ, PYTHONPATH=SDK)
    return subprocess.run(
        [sys.executable, script], input=stdin.encode("utf-8"), capture_output=True, env=env, timeout=60
    )


class RunTest(unittest.TestCase):
    """run() as a real process: exit status, stdout and stderr."""

    SCRIPT = (
        "import sys\n"
        "from custos_processor import Output, OutputTask, run\n"
        "def process(task, answer):\n"
        "    if answer.text == 'fail\\n':\n"
        "        raise ValueError('asked to fail')\n"
        "    return Output(tasks=[OutputTask(match_key='k', title=task.title, answer_type='text')])\n"
        "run(process)\n"
    )

    def setUp(self):
        import tempfile

        fd, self.script = tempfile.mkstemp(suffix=".py")
        with os.fdopen(fd, "w") as f:
            f.write(self.SCRIPT)

    def tearDown(self):
        os.remove(self.script)

    def test_success(self):
        p = run_script(self.script, SAMPLE_INPUT)
        self.assertEqual(p.returncode, 0, p.stderr)
        self.assertEqual(
            p.stdout, b'{"tasks":[{"match_key":"k","title":"Hosts","body":"","answer_type":"text"}],"documents":[]}\n'
        )

    def test_failure(self):
        p = run_script(self.script, SAMPLE_INPUT.replace('"web-01\\n"', '"fail\\n"'))
        self.assertEqual(p.returncode, 1)
        self.assertEqual(p.stdout, b"")
        self.assertEqual(p.stderr.decode(), "asked to fail\n")


if __name__ == "__main__":
    unittest.main()
