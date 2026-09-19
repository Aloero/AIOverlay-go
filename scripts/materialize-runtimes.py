#!/usr/bin/env python3
"""Materialize pinned Windows runtimes from official release archives.

This helper is explicit by design: release/preflight stay fail-closed and never
silently download executable code. Every archive and extracted file is verified
by SHA-256 before replacing an LFS pointer in the working tree.
"""
from __future__ import annotations

import argparse
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import time
import urllib.request
import zipfile

ROOT = Path(__file__).resolve().parents[1]

RUNTIMES = (
    {
        "name": "OpenCode v1.18.31",
        "url": "https://github.com/anomalyco/opencode/releases/download/v1.18.31/opencode-windows-x64.zip",
        "archive_sha256": "0ecd7ffc7f26390ce7799e7bcd409e4f11c410144308a6a5b0fcdce63d871006",
        "files": {
            "opencode.exe": ("runtime/opencode/opencode.exe", "0242a0dc705af67c90882b456a36b619883c1c786aad8fe071a1bc64e5d1d440"),
        },
    },
    {
        "name": "sing-box v1.14.1",
        "url": "https://github.com/SagerNet/sing-box/releases/download/v1.14.1/sing-box-1.14.1-windows-amd64.zip",
        "archive_sha256": "5197f16d492d93202dc623622149a6ed040f8eca263128f91d603f2b901baa89",
        "files": {
            "sing-box.exe": ("runtime/sing-box/sing-box.exe", "b838de45bd0b2e6ddbed1977e4745622f7dffab3b293807ff4c6b1b640fed909"),
            "libcronet.dll": ("runtime/sing-box/libcronet.dll", "3217c6260fbca5f16072e0b79735742f40109a63bb0ff88fd6b96dd6b54a2928"),
        },
    },
)


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def verify_materialized() -> bool:
    ok = True
    for runtime in RUNTIMES:
        for _, (relative, expected) in runtime["files"].items():
            path = ROOT / relative
            if not path.is_file():
                print(f"MISSING {relative}")
                ok = False
                continue
            actual = sha256_file(path)
            if actual != expected:
                print(f"MISMATCH {relative}: {actual} != {expected}")
                ok = False
            else:
                print(f"OK {relative} {actual}")
    return ok


def download(url: str, destination: Path) -> None:
    curl = shutil.which("curl")
    if curl:
        subprocess.run(
            [curl, "-fL", "--retry", "5", "--retry-delay", "2", "--retry-all-errors",
             "--connect-timeout", "20", "--max-time", "900", "-A",
             "AIOverlay-go-runtime-materializer/1", "-o", str(destination), url],
            check=True,
        )
        return
    wget = shutil.which("wget")
    if wget:
        subprocess.run(
            [wget, "--tries=5", "--timeout=30", "--user-agent=AIOverlay-go-runtime-materializer/1",
             "-O", str(destination), url],
            check=True,
        )
        return

    request = urllib.request.Request(url, headers={"User-Agent": "AIOverlay-go-runtime-materializer/1"})
    last_error: Exception | None = None
    for attempt in range(5):
        try:
            with urllib.request.urlopen(request, timeout=90) as response, destination.open("wb") as output:
                shutil.copyfileobj(response, output, 1024 * 1024)
            return
        except Exception as error:  # pragma: no cover - network-specific fallback
            last_error = error
            destination.unlink(missing_ok=True)
            time.sleep(2 * (attempt + 1))
    raise RuntimeError(f"download failed: {url}: {last_error}")


def archive_entries(archive: zipfile.ZipFile) -> dict[str, str]:
    result: dict[str, str] = {}
    for name in archive.namelist():
        base = Path(name).name
        if not base:
            continue
        if base in result:
            raise RuntimeError(f"archive contains duplicate basename {base!r}")
        result[base] = name
    return result


def materialize_runtime(runtime: dict[str, object], cache: Path) -> None:
    name = str(runtime["name"])
    url = str(runtime["url"])
    expected_archive = str(runtime["archive_sha256"])
    archive_path = cache / Path(url).name
    if not archive_path.exists() or sha256_file(archive_path) != expected_archive:
        archive_path.unlink(missing_ok=True)
        print(f"Downloading {name}: {url}")
        download(url, archive_path)
    actual_archive = sha256_file(archive_path)
    if actual_archive != expected_archive:
        raise RuntimeError(f"{name} archive SHA-256 mismatch: {actual_archive} != {expected_archive}")
    print(f"Archive OK {name}: {actual_archive}")

    with zipfile.ZipFile(archive_path) as archive:
        bad_member = archive.testzip()
        if bad_member is not None:
            raise RuntimeError(f"{name} archive CRC failed for {bad_member}")
        entries = archive_entries(archive)
        files = runtime["files"]
        assert isinstance(files, dict)
        for base, pair in files.items():
            relative, expected_file = pair
            member = entries.get(base)
            if not member:
                raise RuntimeError(f"{name}: missing {base} in archive")
            target = ROOT / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            temporary = target.with_name(target.name + ".materializing")
            digest = hashlib.sha256()
            with archive.open(member) as source, temporary.open("wb") as output:
                for chunk in iter(lambda: source.read(1024 * 1024), b""):
                    output.write(chunk)
                    digest.update(chunk)
            actual_file = digest.hexdigest()
            if actual_file != expected_file:
                temporary.unlink(missing_ok=True)
                raise RuntimeError(f"{name} {base} SHA-256 mismatch: {actual_file} != {expected_file}")
            os.replace(temporary, target)
            print(f"Materialized {relative}: {actual_file}")


def populate_lfs_cache() -> None:
    git_lfs = shutil.which("git-lfs")
    git = shutil.which("git")
    if not git_lfs or not git or not (ROOT / ".git").exists():
        return
    subprocess.run([git, "config", "--local", "filter.lfs.clean", "git-lfs clean -- %f"], cwd=ROOT, check=True)
    subprocess.run([git, "config", "--local", "filter.lfs.smudge", "git-lfs smudge -- %f"], cwd=ROOT, check=True)
    subprocess.run([git, "config", "--local", "filter.lfs.process", "git-lfs filter-process"], cwd=ROOT, check=True)
    subprocess.run([git, "config", "--local", "filter.lfs.required", "true"], cwd=ROOT, check=True)
    for runtime in RUNTIMES:
        files = runtime["files"]
        assert isinstance(files, dict)
        for _, (relative, _) in files.items():
            path = ROOT / relative
            with path.open("rb") as source:
                subprocess.run([git_lfs, "clean"], cwd=ROOT, stdin=source, stdout=subprocess.DEVNULL, check=True)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="verify current materialized files without downloading")
    parser.add_argument("--cache", default=str(Path(tempfile.gettempdir()) / "aioverlay-runtime-cache"))
    args = parser.parse_args()
    if args.check:
        return 0 if verify_materialized() else 1
    cache = Path(args.cache)
    cache.mkdir(parents=True, exist_ok=True)
    for runtime in RUNTIMES:
        materialize_runtime(runtime, cache)
    populate_lfs_cache()
    return 0 if verify_materialized() else 1


if __name__ == "__main__":
    raise SystemExit(main())
