"""Write custos processors in Python.

A processor is a container image. custos starts it with the contract input
(custos.processor/v1) on stdin and reads the output from stdout; whatever
the processor writes to stderr is kept as the run's log. With this module a
processor is one function::

    from custos_processor import Output, OutputTask, run

    def process(task, answer):
        return Output(tasks=[OutputTask(match_key="a", title="A", answer_type="text")])

    if __name__ == "__main__":
        run(process)

``run`` reads and checks the input, calls the function, checks the output
with the rules custos applies, and writes it. Only the standard library is
used (Python 3.10 or later).
"""

from __future__ import annotations

import json
import re
import sys
from dataclasses import dataclass, field
from typing import Any, Callable, Optional, TextIO

__all__ = [
    "CONTRACT_VERSION",
    "MAX_OUTPUT_SIZE",
    "ANSWER_TYPES",
    "ContractError",
    "Task",
    "Answer",
    "Attachment",
    "Output",
    "OutputTask",
    "OutputDocument",
    "Origin",
    "read_input",
    "validate",
    "encode",
    "process_stream",
    "run",
]

CONTRACT_VERSION = "custos.processor/v1"
MAX_OUTPUT_SIZE = 16 << 20
ANSWER_TYPES = ("markdown", "text", "timestamp", "path", "url", "choice")
BUMPS = ("patch", "minor", "major")

_UUID_V4 = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$")
_IDENT = r"(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
_SEMVER = re.compile(
    r"^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-" + _IDENT + r"(\." + _IDENT + r")*)?$"
)
_NAME = re.compile(r"^[a-z0-9][a-z0-9-]*$")


class ContractError(Exception):
    """The input or the output breaks the contract. ``problems`` lists every
    problem found."""

    def __init__(self, message: str, problems: Optional[list[str]] = None):
        self.problems = problems or []
        if self.problems:
            message = message + ":\n  " + "\n  ".join(self.problems)
        super().__init__(message)


@dataclass
class Task:
    """The version of the task that was answered."""

    id: str
    version: str
    title: str
    body: str
    answer_type: str
    choices: list[str] = field(default_factory=list)


@dataclass
class Attachment:
    """A file attached to the answer, mounted read-only at ``path``."""

    name: str
    sha256: str
    media_type: str
    path: str


@dataclass
class Answer:
    """The answer the processor works on. ``value`` is None for markdown
    answers, whose text is in ``body``."""

    task_version: str
    value: Optional[str]
    body: str
    attachments: list[Attachment] = field(default_factory=list)

    @property
    def text(self) -> str:
        """The value, or the body for markdown answers."""
        return self.value if self.value is not None else self.body


@dataclass
class Origin:
    """Places a generated task below another task in the book."""

    id: str
    version: Optional[str] = None


@dataclass
class OutputTask:
    """A generated task. ``match_key`` identifies it across runs on the same
    answer. ``choices`` is required for answer type choice and forbidden
    otherwise; ``bump`` defaults to minor on the server."""

    match_key: str
    title: str
    answer_type: str
    body: str = ""
    choices: list[str] = field(default_factory=list)
    origin: Optional[Origin] = None
    processor: Optional[str] = None
    bump: Optional[str] = None


@dataclass
class OutputDocument:
    """A document such as an in-toto statement, a DSSE envelope or a
    Sigstore bundle. ``content`` is any JSON value except None."""

    match_key: str
    name: str
    media_type: str
    content: Any


@dataclass
class Output:
    tasks: list[OutputTask] = field(default_factory=list)
    documents: list[OutputDocument] = field(default_factory=list)


@dataclass
class _Input:
    workspace_id: str
    task: Task
    answer: Answer


def _str(d: dict, key: str) -> str:
    v = d.get(key)
    return v if isinstance(v, str) else ""


def read_input(stream: TextIO) -> _Input:
    """Decodes the contract input and checks its contract version. Fields
    this version does not know are ignored."""
    try:
        data = json.loads(stream.read())
    except ValueError as e:
        raise ContractError(f"reading the input: {e}") from None
    if not isinstance(data, dict):
        raise ContractError("reading the input: not a JSON object")
    if data.get("contract") != CONTRACT_VERSION:
        raise ContractError(
            f"input has contract {json.dumps(data.get('contract'))}; this SDK speaks {CONTRACT_VERSION}"
        )
    t = data.get("task") or {}
    a = data.get("answer") or {}
    w = data.get("workspace") or {}
    task = Task(
        id=_str(t, "id"),
        version=_str(t, "version"),
        title=_str(t, "title"),
        body=_str(t, "body"),
        answer_type=_str(t, "answer_type"),
        choices=list(t.get("choices") or []),
    )
    answer = Answer(
        task_version=_str(a, "task_version"),
        value=a.get("value") if isinstance(a.get("value"), str) else None,
        body=_str(a, "body"),
        attachments=[
            Attachment(name=_str(x, "name"), sha256=_str(x, "sha256"), media_type=_str(x, "media_type"), path=_str(x, "path"))
            for x in (a.get("attachments") or [])
        ],
    )
    return _Input(workspace_id=_str(w, "id"), task=task, answer=answer)


def _blank(v: Any) -> bool:
    return not isinstance(v, str) or v.strip() == ""


def validate(output: Output) -> list[str]:
    """Returns every problem custos would find in ``output``, except whether
    a named processor is registered (only the catalog knows that)."""
    if not isinstance(output, Output):
        return [f"process must return an Output, not {type(output).__name__}"]
    ps: list[str] = []
    keys: set[str] = set()
    for i, t in enumerate(output.tasks):
        at = f"tasks[{i}]"
        if not isinstance(t, OutputTask):
            ps.append(f"{at}: must be an OutputTask, not {type(t).__name__}")
            continue
        if _blank(t.match_key):
            ps.append(f"{at}: match_key is missing")
        elif t.match_key in keys:
            ps.append(f"{at}: match_key {json.dumps(t.match_key)} is used by an earlier task")
        keys.add(t.match_key)
        if _blank(t.title):
            ps.append(f"{at}: title is missing")
        if not isinstance(t.body, str):
            ps.append(f"{at}: body must be a string")
        if t.answer_type not in ANSWER_TYPES:
            ps.append(f"{at}: answer_type {json.dumps(t.answer_type)} is not one of {', '.join(ANSWER_TYPES)}")
        if t.answer_type == "choice":
            if not t.choices:
                ps.append(f"{at}: answer_type choice needs a non-empty choices list")
            seen: set[str] = set()
            for c in t.choices or []:
                if _blank(c):
                    ps.append(f"{at}: choices must not be empty")
                elif c in seen:
                    ps.append(f"{at}: duplicate choice {json.dumps(c)}")
                seen.add(c)
        elif t.choices:
            ps.append(f"{at}: choices are only allowed with answer_type choice")
        if t.origin is not None:
            if not isinstance(t.origin.id, str) or not _UUID_V4.match(t.origin.id):
                ps.append(f"{at}: origin.id {json.dumps(t.origin.id)} is not a lowercase UUID v4")
            if t.origin.version and (not isinstance(t.origin.version, str) or not _SEMVER.match(t.origin.version)):
                ps.append(f"{at}: origin.version {json.dumps(t.origin.version)} is not a semantic version such as 1.2.0")
        if t.processor and (not isinstance(t.processor, str) or not _NAME.match(t.processor)):
            ps.append(f"{at}: processor {json.dumps(t.processor)} must match [a-z0-9][a-z0-9-]*")
        if t.bump and t.bump not in BUMPS:
            ps.append(f"{at}: bump {json.dumps(t.bump)} is not one of patch, minor, major")
    keys = set()
    for i, d in enumerate(output.documents):
        at = f"documents[{i}]"
        if not isinstance(d, OutputDocument):
            ps.append(f"{at}: must be an OutputDocument, not {type(d).__name__}")
            continue
        if _blank(d.match_key):
            ps.append(f"{at}: match_key is missing")
        elif d.match_key in keys:
            ps.append(f"{at}: match_key {json.dumps(d.match_key)} is used by an earlier document")
        keys.add(d.match_key)
        if _blank(d.name):
            ps.append(f"{at}: name is missing")
        if _blank(d.media_type):
            ps.append(f"{at}: media_type is missing")
        if d.content is None:
            ps.append(f"{at}: content is missing")
        else:
            try:
                json.dumps(d.content, allow_nan=False)
            except (TypeError, ValueError) as e:
                ps.append(f"{at}: content cannot be encoded as JSON: {e}")
    return ps


def _task_dict(t: OutputTask) -> dict:
    d: dict[str, Any] = {"match_key": t.match_key, "title": t.title, "body": t.body, "answer_type": t.answer_type}
    if t.choices:
        d["choices"] = list(t.choices)
    if t.origin is not None:
        d["origin"] = {"id": t.origin.id}
        if t.origin.version:
            d["origin"]["version"] = t.origin.version
    if t.processor:
        d["processor"] = t.processor
    if t.bump:
        d["bump"] = t.bump
    return d


def encode(output: Output) -> bytes:
    """Checks ``output`` and returns it as UTF-8 JSON, followed by a
    newline. Raises ContractError when it breaks the contract."""
    problems = validate(output)
    if problems:
        raise ContractError("invalid output", problems)
    doc = {
        "tasks": [_task_dict(t) for t in output.tasks],
        "documents": [
            {"match_key": d.match_key, "name": d.name, "media_type": d.media_type, "content": d.content}
            for d in output.documents
        ],
    }
    data = (json.dumps(doc, ensure_ascii=False, allow_nan=False, separators=(",", ":")) + "\n").encode("utf-8")
    if len(data) > MAX_OUTPUT_SIZE:
        raise ContractError(f"output is {len(data)} bytes; custos accepts at most {MAX_OUTPUT_SIZE}")
    return data


Process = Callable[[Task, Answer], Output]


def process_stream(process: Process, stdin: TextIO, stdout: Any) -> None:
    """``run`` without the process around it: reads the input from
    ``stdin``, calls ``process``, checks the output and writes it to the
    binary stream ``stdout``. Nothing is written when it raises."""
    inp = read_input(stdin)
    data = encode(process(inp.task, inp.answer))
    stdout.write(data)
    stdout.flush()


def run(process: Process) -> None:
    """Runs ``process`` as a processor and exits: status 0 after writing the
    output, status 1 with the error on stderr otherwise."""
    try:
        process_stream(process, sys.stdin, sys.stdout.buffer)
    except Exception as e:  # any failure fails the run; the message is the log
        print(f"{e}", file=sys.stderr)
        sys.exit(1)
    sys.exit(0)
