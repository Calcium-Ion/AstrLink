"""Run synthetic code/privacy cases through an installed native worker, offline.

Example:
  python3 benchmark_model.py --worker /path/to/astrlink-privacy-worker \
      --model-dir /path/to/verified/model --output /tmp/privacy-results.json

This is a small regression set, not an accuracy benchmark or a CI model download.
"""

import argparse
import json
import os
from pathlib import Path
import struct
import subprocess
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--worker", type=Path, required=True)
    parser.add_argument("--model-dir", type=Path, required=True)
    parser.add_argument("--cases", type=Path, default=Path(__file__).parent.parent / "testdata" / "code-privacy-cases.json")
    parser.add_argument("--output", type=Path)
    parser.add_argument("--timeout", type=float, default=180)
    args = parser.parse_args()
    if os.environ.get("ASTRLINK_CI_SYNTHETIC_MODELS_ONLY") or os.environ.get("ASTRLINK_CI_NO_REMOTE_MODELS"):
        parser.error("Real-model benchmarks must run explicitly outside CI.")
    cases = json.loads(args.cases.read_text(encoding="utf-8"))
    payload = json.dumps({"version": 1, "id": 1, "texts": [
        {"id": index, "text": case["text"]} for index, case in enumerate(cases)
    ]}, ensure_ascii=False).encode()
    started = time.perf_counter()
    result = subprocess.run(
        [str(args.worker.resolve()), "--model-dir", str(args.model_dir.resolve())],
        input=struct.pack(">I", len(payload)) + payload, capture_output=True,
        timeout=args.timeout, check=True,
    )
    elapsed = time.perf_counter() - started
    frames = []
    offset = 0
    while offset < len(result.stdout):
        if len(result.stdout) - offset < 4:
            raise ValueError("Truncated worker frame")
        size = struct.unpack_from(">I", result.stdout, offset)[0]
        offset += 4
        if size == 0 or size > 64 * 1024 * 1024 or size > len(result.stdout) - offset:
            raise ValueError("Invalid worker frame")
        frames.append(json.loads(result.stdout[offset:offset + size]))
        offset += size
    if len(frames) != 2 or frames[0] != {"version": 1, "ready": True}:
        raise ValueError("Invalid worker handshake")
    response = frames[1]
    if response.get("error") or response.get("id") != 1 or response.get("version") != 1:
        raise ValueError(f"Worker did not complete inference: {response.get('error')}")
    rows = []
    total_tp = total_fp = total_fn = false_positive_cases = clean_cases = 0
    for index, case in enumerate(cases):
        text = case["text"]
        encoded = text.encode()
        expected = set()
        for entity in case["entities"]:
            position = text.index(entity["value"])
            start = len(text[:position].encode())
            expected.add((entity["label"], start, start + len(entity["value"].encode())))
        found = set()
        spans = []
        for span in response["spans"]:
            if span["text_id"] != index:
                continue
            start, end = span["start"], span["end"]
            if not 0 <= start < end <= len(encoded):
                raise ValueError("Invalid worker span")
            value = encoded[start:end].decode()
            found.add((span["label"], start, end))
            spans.append({**span, "value": value})
        tp, fp, fn = len(found & expected), len(found - expected), len(expected - found)
        total_tp += tp
        total_fp += fp
        total_fn += fn
        if not expected:
            clean_cases += 1
            false_positive_cases += bool(found)
        rows.append({"case": case["id"], "tp": tp, "fp": fp, "fn": fn, "spans": spans})
    report = {
        "scope": "Synthetic code/privacy regression examples; not representative accuracy",
        "model_identity": json.loads((args.model_dir / "astrlink-model.json").read_text())["identity"],
        "cases": len(cases), "clean_cases": clean_cases, "false_positive_cases": false_positive_cases,
        "strict_span_tp": total_tp, "strict_span_fp": total_fp, "strict_span_fn": total_fn,
        "elapsed_seconds_including_startup": round(elapsed, 3), "results": rows,
    }
    if args.output:
        args.output.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(json.dumps({key: value for key, value in report.items() if key != "results"}, indent=2, ensure_ascii=False))


if __name__ == "__main__":
    main()
