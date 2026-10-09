"""Example custos processor, the Python twin of sdk/go/examples/hostlist.

Reads an answer that lists host names, one per line (Markdown list markers
and # comments are allowed), and produces one follow-up task per host that
asks when the host was last patched, plus an in-toto statement with the
hosts as subjects.
"""

import hashlib
import re

from custos_processor import Answer, Output, OutputDocument, OutputTask, Task, run

HOST = re.compile(r"^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$")


def parse_hosts(text: str) -> list[str]:
    hosts: list[str] = []
    for i, line in enumerate(text.split("\n"), start=1):
        line = line.strip()
        for marker in ("- ", "* "):
            if line.startswith(marker):
                line = line[len(marker):].strip()
        if not line or line.startswith("#"):
            continue
        host = line.lower()
        if not HOST.match(host):
            raise ValueError(f"line {i}: {line!r} is not a host name")
        if host not in hosts:
            hosts.append(host)
    if not hosts:
        raise ValueError("the answer lists no hosts; write one host name per line")
    return hosts


def process(task: Task, answer: Answer) -> Output:
    hosts = parse_hosts(answer.text)
    tasks = [
        OutputTask(
            match_key=f"host:{h}",
            title=f"Patch level of {h}",
            body=f"When was {h} last patched? Give the time of the last update.",
            answer_type="timestamp",
        )
        for h in hosts
    ]
    statement = {
        "_type": "https://in-toto.io/Statement/v1",
        "subject": [{"name": h, "digest": {"sha256": hashlib.sha256(h.encode()).hexdigest()}} for h in hosts],
        "predicateType": "https://example.org/custos/hostlist/v1",
        "predicate": {"task": f"{task.id}@{task.version}", "hosts": len(hosts)},
    }
    doc = OutputDocument(
        match_key="inventory", name="Host inventory", media_type="application/vnd.in-toto+json", content=statement
    )
    return Output(tasks=tasks, documents=[doc])


if __name__ == "__main__":
    run(process)
