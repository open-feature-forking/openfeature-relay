#!/usr/bin/env python3
"""Prints the output of the failed tests of a go test -json run, and keeps its whole output as a go test log (#190).

usage: testbed-failures.py <run.json> <run.log>

The log is what test-failures.py reads. Report-only: the exit code is 0. Lines that are not JSON (a build failure of an
older toolchain) are kept in the log as they are.
"""
import json
import sys


def main(argv):
    if len(argv) != 3:
        print(__doc__)
        return 2
    outputs, failed, text = {}, [], []
    with open(argv[1], encoding="utf-8", errors="replace") as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                text.append(line if line.endswith("\n") else line + "\n")
                continue
            if not isinstance(event, dict):  # a scalar line, such as a bare number
                continue
            key = (event.get("Package", ""), event.get("Test", ""))
            if event.get("Action") == "build-output":  # the compile errors of Go 1.27, which have no Package or Test
                text.append(event.get("Output", ""))
            elif event.get("Action") == "output":
                text.append(event.get("Output", ""))
                outputs.setdefault(key, []).append(event.get("Output", ""))
            elif event.get("Action") == "fail" and key not in failed:
                failed.append(key)
    with open(argv[2], "w", encoding="utf-8") as stream:
        stream.write("".join(text))
    for package, test in failed:
        if test:  # a package's own failure is the FAIL line test-failures.py lists
            print(f"=== {package} {test}")
            print("".join(outputs.get((package, test), [])).rstrip("\n"))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
