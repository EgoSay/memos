#!/usr/bin/env python3
"""Create a verified directory snapshot of this journal's SQLite and local media.

Uploaded media in Kairos has unique, immutable paths. Snapshot the database first,
then retain those exact media versions, including originals used by revisions.
A concurrent deletion that wins the race makes this attempt fail, never succeed
with missing media. Retry from a new database snapshot. No application pause or
live SQLite file copying is required. S3/external attachments are unsupported.
"""
from __future__ import annotations

import argparse
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sqlite3
import tempfile
import time

FORMAT = "memos-directory-snapshot-v1"
UID = re.compile(r"^[a-zA-Z0-9_-]+$")
DATABASE = "memos_prod.db"


def checksum(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def required_media(db: sqlite3.Connection, logical_root: Path) -> set[str]:
    """Resolve only paths within the data root; reject unsupported dependencies."""
    paths = set()
    for uid, storage, reference in db.execute("SELECT uid,storage_type,reference FROM attachment"):
        if not UID.fullmatch(uid):
            raise ValueError("invalid attachment UID")
        if storage in ("S3", "EXTERNAL"):
            raise ValueError("remote media requires an independent, verified backup")
        if storage == "LOCAL":
            path = Path(reference)
            if path.is_absolute():
                path = path.relative_to(logical_root)
            if not path.parts or ".." in path.parts:
                raise ValueError("attachment reference escapes the data directory")
            paths.add(path.as_posix())
        elif storage not in ("", "DATABASE"):
            raise ValueError("unrecognized attachment storage")
        # Original bytes are part of the journal's archival contract, even for
        # transformed pictures or attachments whose served bytes live in SQLite.
        paths.add("originals/" + uid)
    for (raw,) in db.execute("SELECT payload FROM journal_document WHERE kind='revision'"):
        for attachment in json.loads(raw)["memo"].get("attachments", []):
            name = attachment["name"]
            if not name.startswith("attachments/") or not UID.fullmatch(name[12:]):
                raise ValueError("invalid historical attachment name")
            if attachment.get("externalLink"):
                raise ValueError("historical external media is not backed up")
            paths.add("originals/" + name[12:])
    return paths


def retain_file(source: Path, target: Path) -> None:
    """Pin immutable media in the staging tree, with a copy fallback across disks."""
    if source.is_symlink() or not source.is_file():
        raise ValueError("missing media or symbolic link")
    target.parent.mkdir(parents=True, exist_ok=True)
    before = source.stat()
    try:
        os.link(source, target, follow_symlinks=False)
    except OSError as error:
        if error.errno != 18:  # EXDEV; other errors must fail closed.
            raise
        shutil.copy2(source, target)
    if target.is_symlink() or not target.is_file():
        raise ValueError("media changed into an unsafe file")
    after = target.stat()
    if (before.st_size, before.st_mtime_ns) != (after.st_size, after.st_mtime_ns):
        raise ValueError("media changed during snapshot")


def snapshot(source: Path, destination: Path, logical_root: Path | None = None) -> dict:
    source, destination = source.resolve(), destination.resolve()
    logical_root = logical_root or source
    if not source.is_dir() or destination.exists() or destination.is_relative_to(source):
        raise ValueError("snapshot requires an existing source and a new external destination")
    if (source / DATABASE).is_symlink() or not (source / DATABASE).is_file():
        raise ValueError("source database missing or symbolic")
    destination.parent.mkdir(parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix=".journal-snapshot-", dir=destination.parent))
    started = int(time.time())
    deadline = time.monotonic() + 180

    def progress(*_):
        if time.monotonic() > deadline:
            raise TimeoutError("SQLite snapshot timed out")

    try:
        data = stage / "data"
        data.mkdir(mode=0o700)
        with closing(sqlite3.connect((source / DATABASE).as_uri() + "?mode=ro", uri=True)) as original:
            with closing(sqlite3.connect(data / DATABASE)) as copied:
                original.backup(copied, pages=256, progress=progress, sleep=0.05)
                if copied.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
                    raise ValueError("SQLite integrity check failed")
                required = required_media(copied, logical_root)
                counts = {table: copied.execute(f'SELECT count(*) FROM "{table}"').fetchone()[0]
                          for table in ("user", "memo", "attachment", "journal_document")}
        for relative in sorted(required):
            original = source / relative
            if original.resolve().is_relative_to(source) is False:
                raise ValueError("media path escapes the data directory")
            # Checking every ancestor prevents a symlinked directory from
            # quietly changing the snapshot scope.
            if any(part.is_symlink() for part in (original, *original.parents) if part != source.parent):
                raise ValueError("symbolic media path")
            retain_file(original, data / relative)
        files = {path.relative_to(stage).as_posix(): {"bytes": path.stat().st_size, "sha256": checksum(path)}
                 for path in sorted(data.rglob("*")) if path.is_file()}
        report = {"format": FORMAT, "sourceTs": started, "completedTs": int(time.time()),
                  "database": DATABASE, "logicalRoot": str(logical_root), "counts": counts, "files": files}
        (stage / "manifest.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
        verify(stage)
        os.replace(stage, destination)
        return report
    finally:
        if stage.exists():
            shutil.rmtree(stage)


def verify(directory: Path) -> dict:
    directory = directory.resolve()
    report = json.loads((directory / "manifest.json").read_text())
    if report["format"] != FORMAT:
        raise ValueError("unknown snapshot format")
    if "data/" + DATABASE not in report["files"]:
        raise ValueError("snapshot manifest omits the database")
    for relative, expected in report["files"].items():
        path = directory / relative
        if (not relative.startswith("data/") or ".." in Path(relative).parts
                or not path.resolve().is_relative_to(directory)
                or any(part.is_symlink() for part in (path, *path.parents) if part.is_relative_to(directory))):
            raise ValueError("unsafe snapshot path")
        if path.stat().st_size != expected["bytes"] or checksum(path) != expected["sha256"]:
            raise ValueError("snapshot content does not match its manifest")
    with closing(sqlite3.connect((directory / "data" / DATABASE).as_uri() + "?mode=ro", uri=True)) as db:
        if db.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
            raise ValueError("snapshot database is corrupt")
        required = required_media(db, Path(report["logicalRoot"]))
        if any("data/" + path not in report["files"] for path in required):
            raise ValueError("snapshot manifest omits referenced media")
        for table, count in report["counts"].items():
            if table not in ("user", "memo", "attachment", "journal_document"):
                raise ValueError("unknown count table")
            if db.execute(f'SELECT count(*) FROM "{table}"').fetchone()[0] != count:
                raise ValueError("snapshot database counts differ")
    return report


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("create", "verify"))
    parser.add_argument("--source", type=Path)
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--logical-root", type=Path)
    args = parser.parse_args()
    try:
        report = (snapshot(args.source, args.destination, args.logical_root)
                  if args.action == "create" else verify(args.destination))
        print(json.dumps({key: value for key, value in report.items() if key != "files"}))
    except (OSError, ValueError, KeyError, sqlite3.Error, TimeoutError) as error:
        parser.exit(1, type(error).__name__ + ": snapshot failed\n")
