#!/usr/bin/env python3
"""Refresh the bundled model catalog and the embedded-OpenCode allow-list.

Two different upstreams drift independently, and both are needed:

* ``runtime/catalog/models-dev-api.json``  - metadata (names, modalities, limits, cost)
  from https://models.dev/api.json
* ``runtime/catalog/opencode-enabled.json`` - the refs the embedded OpenCode runtime can
  actually serve right now, from the provider's own model endpoint. The app treats this file
  as the allow-list of keyless OpenCode models (see internal/catalog/routes.go), so refs that
  upstream has retired make every request fail with a provider error that surfaces to the user
  as an opaque timeout.

The script is explicit and read-only unless ``--write`` is given, and it never touches
executable code.

  python3 scripts/refresh-catalog.py            # report drift, write nothing
  python3 scripts/refresh-catalog.py --write    # replace both files
"""
from __future__ import annotations

import argparse
import json
import re
import sys
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CATALOG = ROOT / "runtime" / "catalog" / "models-dev-api.json"
ENABLED = ROOT / "runtime" / "catalog" / "opencode-enabled.json"
METADATA_URL = "https://models.dev/api.json"
LIVE_URL = "https://opencode.ai/zen/v1/models"
PROVIDER = "opencode"


def fetch(url: str) -> object:
    req = urllib.request.Request(url, headers={"User-Agent": "AIOverlay-go catalog refresh"})
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.loads(resp.read().decode("utf-8"))


def rows(live_ids: set[str], catalog: dict) -> list[dict]:
    models = catalog.get(PROVIDER, {}).get("models", {})
    out = []
    for ref_id in sorted(live_ids):
        meta = models.get(ref_id)
        if meta is None:
            continue  # served by upstream, no metadata: the app could not describe it
        cost = meta.get("cost") or {}
        modalities = (meta.get("modalities") or {})
        out.append({
            "ref": f"{PROVIDER}/{ref_id}",
            "name": meta.get("name") or ref_id,
            "vision": "image" in (modalities.get("input") or []),
            "reasoning": bool(meta.get("reasoning")),
            "cost": {
                "input": float(cost.get("input", 0) or 0),
                "output": float(cost.get("output", 0) or 0),
                "cache": {"read": float((cost.get("cache") or {}).get("read", 0) or 0),
                          "write": float((cost.get("cache") or {}).get("write", 0) or 0)},
            },
        })
    return out


def dump(data: object) -> str:
    """Allow-list formatting: one entry per few lines, reviewable in a diff."""
    return json.dumps(data, ensure_ascii=False, indent=2) + "\n"


def dump_compact(data: object) -> str:
    """Vendored upstream snapshot: byte-for-byte the shape models.dev serves, one line."""
    return json.dumps(data, ensure_ascii=False, separators=(",", ":"))


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--write", action="store_true", help="replace the two catalog files")
    ap.add_argument("--free-only", action="store_true", help="keep only zero-cost models in the allow-list")
    args = ap.parse_args()

    live_raw = fetch(LIVE_URL)
    entries = live_raw.get("data", []) if isinstance(live_raw, dict) else live_raw
    live_ids = {m["id"] for m in entries if isinstance(m, dict) and m.get("id")}
    catalog = fetch(METADATA_URL)
    have = catalog.get(PROVIDER, {}).get("models", {})
    missing = sorted(live_ids - set(have))
    if missing:
        print(f"warning: {len(missing)} served models have no models.dev metadata: {missing[:6]}")

    table = rows(live_ids, catalog)
    if args.free_only:
        table = [r for r in table if r["cost"]["input"] == 0 and r["cost"]["output"] == 0]

    old = json.loads(ENABLED.read_text(encoding="utf-8")) if ENABLED.exists() else {"models": []}
    old_refs = [m["ref"] for m in old.get("models", [])]
    new_refs = [r["ref"] for r in table]
    retired = [r for r in old_refs if r not in set(new_refs)]
    added = [r for r in new_refs if r not in set(old_refs)]
    vision = [r["ref"] for r in table if r["vision"]]

    print(f"upstream serves {len(live_ids)} models, {len(table)} have metadata")
    print(f"allow-list {len(old_refs)} -> {len(new_refs)} (vision-capable: {len(vision)})")
    print("retired by upstream: " + (", ".join(x.split('/', 1)[1] for x in retired) or "none"))
    print("newly available:     " + (", ".join(x.split('/', 1)[1] for x in added[:12]) or "none"))
    if not args.write:
        print("\ndry run: nothing written (use --write)")
        return 0

    first = (ROOT / "runtime" / "opencode" / "VERSION.txt").read_text(encoding="utf-8").splitlines()[0]
    match = re.search(r"v?(\d+\.\d+\.\d+)", first)
    if match is None:
        print(f"error: cannot read the pinned OpenCode version from {first!r}", file=sys.stderr)
        return 1
    ENABLED.write_text(dump({"version": match.group(1), "models": table}), encoding="utf-8", newline="\n")
    CATALOG.write_text(dump_compact(catalog), encoding="utf-8", newline="\n")
    print(f"\nwrote {ENABLED.relative_to(ROOT)} and {CATALOG.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
