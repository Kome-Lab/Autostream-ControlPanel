#!/usr/bin/env python3
"""Require the fixed polling/create/upload inventory and actual positive events."""
import argparse
from collections import Counter
import hashlib
import json
from pathlib import Path
import re
import shlex

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = Path(__file__).with_name("polling-create-upload-inventory.json")
SQL_PARENT_COUNTS = {"http_parents": 6, "store_parents": 5, "database_parents": 1, "browser_parents": 1}


def require(condition, reason):
    if not condition:
        raise SystemExit(reason)


def inventory():
    data = json.loads(MANIFEST.read_text(encoding="utf-8"))
    require(data["schema_version"] == 1, "unsupported evidence inventory")
    for key in ("unit", "live_cover", "media_browser", "related_browser"):
        scope = data[key]
        require(scope["expected_count"] == len(scope["names"]) > 0, key + " denominator mismatch")
        require(len(set(scope["names"])) == len(scope["names"]), key + " duplicate names")
        require(all(isinstance(name, str) and name and "\n" not in name for name in scope["names"]), key + " invalid name")
    sql = data.get("sql")
    require(isinstance(sql, dict) and set(sql) == set(SQL_PARENT_COUNTS), "SQL parent families differ from the fixed inventory")
    sql_names = []
    for family, count in SQL_PARENT_COUNTS.items():
        parents = sql[family]
        require(isinstance(parents, list) and len(parents) == count > 0, family + " denominator mismatch")
        require(all(isinstance(name, str) and name and name == name.strip() and "\n" not in name and "\r" not in name for name in parents), family + " invalid parent name")
        require(len(set(parents)) == count, family + " duplicate parent names")
        sql_names.extend(parents)
    require(len(sql_names) == len(set(sql_names)) == 13, "SQL parents overlap across families")
    expected_paths = set(data["unit"]["entries"] + data["unit"]["transitive_test_files"] + data["live_cover"]["entries"] + data["media_browser"]["entries"])
    observed_paths = {path.relative_to(ROOT / "web").as_posix() for pattern in data["test_path_patterns"] for path in (ROOT / "web/tests").glob(pattern)}
    require(expected_paths == observed_paths, "polling/create/upload test path inventory changed; review its exact manifest")
    for path in expected_paths | {data["related_browser"]["entry"], *data["related_browser"]["case_sources"]}:
        require((ROOT / "web" / path).is_file(), "required evidence source is missing")
    expected_command = ["node", "--no-warnings", "--test", "--test-reporter=spec", "--test-concurrency=1", "--test-timeout=30000", *data["unit"]["entries"]]
    package = json.loads((ROOT / "web/package.json").read_text(encoding="utf-8"))
    require(shlex.split(package["scripts"]["test:polling-create-upload"]) == expected_command, "unit command differs from its fixed path inventory")
    return data


def verify_spec(data, scope, path):
    raw = path.read_bytes()
    require(len(raw) <= 50 * 1024 * 1024, "oversized test output")
    text = re.sub(r"\x1b\[[0-9;]*m", "", raw.decode("utf-8"))
    expected = data[scope]
    summaries = {}
    for key in ("tests", "suites", "pass", "fail", "cancelled", "skipped", "todo"):
        values = re.findall(r"^\s*ℹ " + key + r" ([0-9]+)\s*$", text, re.M)
        require(len(values) == 1, "missing or duplicate " + key + " summary")
        summaries[key] = int(values[0])
    require(summaries["tests"] == summaries["pass"] == expected["expected_count"], "actual test denominator differs from the fixed inventory")
    require(all(summaries[key] == 0 for key in ("fail", "cancelled", "skipped", "todo")), "non-positive test result")
    names = re.findall(r"^\s*✔ (.+) \([0-9.]+ms\)\s*$", text, re.M)
    require(Counter(names) == Counter(expected["names"]), "actual positive named-test inventory differs")
    result = {"scope": scope, "status": "PASS", "summary": summaries, "positive_names": names, "input_sha256": hashlib.sha256(raw).hexdigest()}
    if scope == "media_browser":
        receipts = []
        for condition in ("ja-light-1440", "en-dark-390"):
            directory = path.parent / "media-upload" / condition
            receipt_raw = (directory / "receipt.json").read_bytes()
            value = json.loads(receipt_raw)
            require(value["status"] == "PASS_ACTUAL_UI_SYNTHETIC_API_FIXTURE" and value["condition"]["id"] == condition and value["create_count"] == 1, "P3 actual UI receipt missing")
            require(value["browser"].get("product", "").startswith("Chrome/"), "P3 actual Chrome version missing")
            require(any(directory.glob("*.png")), "P3 browser screenshots missing")
            receipts.append({"condition": condition, "sha256": hashlib.sha256(receipt_raw).hexdigest(), "browser": value["browser"]})
        require(len(list((path.parent / "media-upload/late-generation").glob("*new-generation-after-old-response.png"))) == 1, "P3 late-generation screenshot missing")
        result["browser_receipts"] = receipts
    return result


def verify_related(data, path):
    raw = path.read_bytes()
    require(len(raw) <= 50 * 1024 * 1024, "oversized browser receipt")
    value = json.loads(raw)
    expected = data["related_browser"]
    require(value["status"] == "PASS_RELATED_ACTUAL_NEXT_UI_FIXTURE", "related browser did not finish positively")
    require(value["expected_cases"] == expected["expected_count"], "related browser denominator changed")
    require(value["counts"] == {"passed": expected["expected_count"], "failed": 0, "not_run": 0, "skipped": 0, "cancelled": 0}, "related browser has non-positive cases")
    require(Counter(case["name"] for case in value["cases"]) == Counter(expected["names"]), "related browser named inventory changed")
    require(all(case["status"] == "PASS" for case in value["cases"]), "related browser case did not pass")
    require(value["source_before"] == value["source_after"], "product source changed during browser proof")
    require(isinstance(value["browser"], dict) and value["browser"].get("product", "").startswith("Chrome/"), "actual Chrome version receipt is missing")
    return {"scope": "related_browser", "status": "PASS", "counts": value["counts"], "positive_names": [case["name"] for case in value["cases"]], "browser": value["browser"], "input_sha256": hashlib.sha256(raw).hexdigest()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("inventory", "sql-http", "sql-store", "spec", "related"))
    parser.add_argument("--scope", choices=("unit", "live_cover", "media_browser"))
    parser.add_argument("--input", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    data = inventory()
    if args.mode == "inventory":
        print(json.dumps({"status": "FIXED_INVENTORY_VALID", "counts": {key: data[key]["expected_count"] for key in ("unit", "live_cover", "media_browser", "related_browser")}}))
        return
    if args.mode == "sql-http":
        parents = data["sql"]["http_parents"]
        require(len(parents) > 0 and len(parents) == len(set(parents)), "invalid SQL HTTP parent inventory")
        print("\n".join(parents))
        return
    if args.mode == "sql-store":
        print(json.dumps({"status": "VALIDATED_SQL_PARENT_INVENTORY", "family_counts": SQL_PARENT_COUNTS, "total": 13,
                          "inventory_sha256": hashlib.sha256(MANIFEST.read_bytes()).hexdigest(), "parents": data["sql"]["store_parents"]}))
        return
    require(args.input is not None and args.output is not None, "input and a fresh output path are required")
    if args.mode == "spec":
        require(args.scope is not None, "spec scope is required")
        result = verify_spec(data, args.scope, args.input)
    else:
        result = verify_related(data, args.input)
    result["inventory_sha256"] = hashlib.sha256(MANIFEST.read_bytes()).hexdigest()
    with args.output.open("x", encoding="utf-8", newline="\n") as stream:
        json.dump(result, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    print(json.dumps({"scope": result["scope"], "status": "PASS", "positive_names": len(result["positive_names"])}))


if __name__ == "__main__":
    main()
