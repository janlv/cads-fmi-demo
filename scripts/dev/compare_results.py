#!/usr/bin/env python3
"""Compare runner result JSON files between two directories (golden regression check).

Files are paired by name; a leading "before_"/"after_" prefix is ignored so the outputs of
the golden capture loop (before_<workflow>.json / after_<workflow>.json) pair up.

Rules: the top-level "_run" pseudo-step is ignored (timings and provenance always differ);
object keys must match exactly; numbers compare with a relative tolerance (default 1e-6, plus
a tiny absolute tolerance for values near zero); everything else must be equal.

Usage: scripts/dev/compare_results.py BEFORE_DIR AFTER_DIR [--rel-tol 1e-6] [--abs-tol 1e-12]
Exit status: 0 all equal, 1 differences or missing files, 2 usage/parse errors.
"""

import argparse
import json
import math
import os
import sys

PREFIXES = ("before_", "after_")
IGNORED_TOP_LEVEL = {"_run"}


def pair_key(name):
    for prefix in PREFIXES:
        if name.startswith(prefix):
            return name[len(prefix):]
    return name


def load_json(path):
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    try:
        return json.loads(text)
    except ValueError:
        pass
    # Tolerate log lines around the JSON object: decode from the first "{" that parses.
    decoder = json.JSONDecoder()
    start = text.find("{")
    while start != -1:
        try:
            value, _ = decoder.raw_decode(text[start:])
            return value
        except ValueError:
            start = text.find("{", start + 1)
    raise ValueError("no JSON object found")


def is_number(value):
    return isinstance(value, (int, float)) and not isinstance(value, bool)


def compare(a, b, path, rel_tol, abs_tol, diffs):
    if is_number(a) and is_number(b):
        if isinstance(a, float) and isinstance(b, float) and math.isnan(a) and math.isnan(b):
            return
        if not math.isclose(a, b, rel_tol=rel_tol, abs_tol=abs_tol):
            diffs.append("%s: %r != %r" % (path, a, b))
        return
    if isinstance(a, dict) and isinstance(b, dict):
        only_a = sorted(set(a) - set(b))
        only_b = sorted(set(b) - set(a))
        if only_a:
            diffs.append("%s: keys only in before: %s" % (path, ", ".join(only_a)))
        if only_b:
            diffs.append("%s: keys only in after: %s" % (path, ", ".join(only_b)))
        for key in sorted(set(a) & set(b)):
            compare(a[key], b[key], "%s.%s" % (path, key) if path else key, rel_tol, abs_tol, diffs)
        return
    if isinstance(a, list) and isinstance(b, list):
        if len(a) != len(b):
            diffs.append("%s: length %d != %d" % (path, len(a), len(b)))
            return
        for index, (x, y) in enumerate(zip(a, b)):
            compare(x, y, "%s[%d]" % (path, index), rel_tol, abs_tol, diffs)
        return
    if type(a) is not type(b) or a != b:
        diffs.append("%s: %r != %r" % (path, a, b))


def json_files(directory):
    files = {}
    for name in sorted(os.listdir(directory)):
        if name.endswith(".json"):
            files[pair_key(name)] = os.path.join(directory, name)
    return files


def main(argv):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("before")
    parser.add_argument("after")
    parser.add_argument("--rel-tol", type=float, default=1e-6)
    parser.add_argument("--abs-tol", type=float, default=1e-12)
    parser.add_argument("--max-diffs", type=int, default=20, help="differences printed per file")
    args = parser.parse_args(argv)

    for directory in (args.before, args.after):
        if not os.path.isdir(directory):
            print("not a directory: %s" % directory, file=sys.stderr)
            return 2

    before = json_files(args.before)
    after = json_files(args.after)
    failed = False

    for key in sorted(set(before) - set(after)):
        print("MISSING in after: %s" % key)
        failed = True
    for key in sorted(set(after) - set(before)):
        print("MISSING in before: %s" % key)
        failed = True

    compared = 0
    for key in sorted(set(before) & set(after)):
        try:
            a = load_json(before[key])
            b = load_json(after[key])
        except (OSError, ValueError) as exc:
            print("ERROR %s: %s" % (key, exc))
            failed = True
            continue
        if isinstance(a, dict):
            a = {k: v for k, v in a.items() if k not in IGNORED_TOP_LEVEL}
        if isinstance(b, dict):
            b = {k: v for k, v in b.items() if k not in IGNORED_TOP_LEVEL}
        diffs = []
        compare(a, b, "", args.rel_tol, args.abs_tol, diffs)
        compared += 1
        if diffs:
            failed = True
            print("DIFF %s (%d differences)" % (key, len(diffs)))
            for line in diffs[: args.max_diffs]:
                print("  " + line)
            if len(diffs) > args.max_diffs:
                print("  ... %d more" % (len(diffs) - args.max_diffs))
        else:
            print("OK   %s" % key)

    print("%d file(s) compared, %s" % (compared, "differences found" if failed else "all equal"))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
