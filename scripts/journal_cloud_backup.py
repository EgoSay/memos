#!/usr/bin/env python3
"""Run a serialized, verified journal snapshot into an encrypted restic repository.

The private JSON configuration supplies source, logicalRoot, workDir, repository,
passwordFile and credentials (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY). Keep it
outside Git with mode 0600. Initialize the repository explicitly before enabling
the systemd timer. No incomplete snapshot advances last-success.json. Receipt
delivery is tracked separately: a monitoring outage does not undo a valid backup.
"""
from __future__ import annotations

import argparse
import fcntl
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import urllib.request

from journal_snapshot import snapshot, verify


def atomic_json(path: Path, value: dict) -> None:
    fd, name = tempfile.mkstemp(prefix=".receipt-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(value, stream, ensure_ascii=False)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(name, path)
    finally:
        Path(name).unlink(missing_ok=True)


def restic(config: dict, *arguments: str, cwd: Path | None = None) -> str:
    environment = dict(os.environ, **config.get("credentials", {}),
                       RESTIC_REPOSITORY=config["repository"],
                       RESTIC_PASSWORD_FILE=config["passwordFile"],
                       RESTIC_CACHE_DIR=str(Path(config["workDir"]) / "cache"),
                       GOMEMLIMIT="256MiB")
    process = subprocess.run([config.get("restic", "restic"), *arguments],
                             env=environment, cwd=cwd, capture_output=True, text=True, timeout=900)
    if process.returncode:
        # Some read errors still create an incomplete restic snapshot (exit 3).
        # Never echo raw tool output: configuration and paths can be private.
        raise RuntimeError("restic failed with exit " + str(process.returncode))
    return process.stdout


def publish_receipt(config: dict, receipt: dict) -> None:
    if config.get("reportUrl"):
        request = urllib.request.Request(config["reportUrl"], data=json.dumps(receipt).encode(),
                                         headers={"Content-Type": "application/json",
                                                  "User-Agent": "Memos-Backup/1.0",
                                                  "Authorization": "Bearer " + config["reportToken"]}, method="POST")
        with urllib.request.urlopen(request, timeout=20) as response:
            if response.status != 200:
                raise RuntimeError("remote receipt was not accepted")


def run(config: dict, action: str = "backup") -> dict:
    work = Path(config["workDir"])
    work.mkdir(parents=True, exist_ok=True, mode=0o700)
    with (work / "lock").open("a") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return {"status": "skipped", "reason": "another backup is running"}
        if action == "init":
            restic(config, "init")
            return {"status": "initialized"}
        if action == "check":
            restic(config, "check", "--read-data")
            result = {"status": "verified", "checkedTs": int(time.time())}
            atomic_json(work / "last-check.json", result)
            return result
        if action == "retain":
            restic(config, "forget", "--host", config.get("host", "memos-production"), "--tag", "journal",
                   "--group-by", "host,paths", "--keep-within", "2d", "--keep-daily", "30",
                   "--keep-monthly", "12", "--keep-yearly", "100", "--prune")
            return {"status": "retained"}
        stage = work / "current"
        try:
            if stage.exists():
                shutil.rmtree(stage)
            report = snapshot(Path(config["source"]), stage, Path(config["logicalRoot"]))
            output = restic(config, "backup", "--json", "--host", config.get("host", "memos-production"),
                            "--tag", "journal", "data", "manifest.json", cwd=stage)
            messages = [json.loads(line) for line in output.splitlines() if line.strip()]
            summary = next(item for item in reversed(messages) if item.get("message_type") == "summary")
            identifier = summary["snapshot_id"]
            if not identifier or not json.loads(restic(config, "snapshots", "--json", identifier)):
                raise RuntimeError("remote snapshot was not confirmed")
            verify(stage)  # Catch unexpected mutation of a retained source inode.
            receipt = {"status": "success", "snapshotId": identifier, "sourceTs": report["sourceTs"],
                       "completedTs": int(time.time()), "mediaComplete": True,
                       "instanceVersion": config.get("instanceVersion", "unknown"),
                       "sourceHost": config.get("host", "memos-production")}
            atomic_json(work / "last-success.json", receipt)
            atomic_json(work / "last-attempt.json", receipt)
        except Exception as error:
            failed = {"status": "failed", "attemptTs": int(time.time()), "errorType": type(error).__name__}
            atomic_json(work / "last-attempt.json", failed)
            try:
                publish_receipt(config, failed)
            except Exception:
                pass
            raise
        finally:
            if stage.exists():
                shutil.rmtree(stage)
        if config.get("reportUrl"):
            try:
                publish_receipt(config, receipt)
            except Exception:
                atomic_json(work / "last-report.json", {"status": "failed", "attemptTs": int(time.time())})
                raise
            atomic_json(work / "last-report.json", {"status": "success", "completedTs": int(time.time())})
        return receipt


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("init", "backup", "check", "retain"))
    parser.add_argument("--config", type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    try:
        print(json.dumps(run(json.loads(args.config.read_text()), args.action)))
    except Exception as error:
        parser.exit(1, type(error).__name__ + ": backup command failed; inspect private backup and delivery receipts\n")
