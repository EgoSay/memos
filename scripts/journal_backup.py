#!/usr/bin/env python3
"""Stopped-instance SQLite backup and isolated, fail-closed restoration.

This private instance archive includes account data and credentials. It is not
an ordinary content export. Store it on an independent, access-controlled disk.
"""
from __future__ import annotations

import argparse
from contextlib import closing
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import secrets
import shutil
import sqlite3
import tempfile
import time
import zipfile

FORMAT = "memos-private-instance-v1"
MAX_BYTES = 32 * 1024**3
MAX_ENTRIES = 100000


def digest(path: Path) -> str:
    result = hashlib.sha256()
    with path.open("rb") as file:
        for data in iter(lambda: file.read(1024 * 1024), b""):
            result.update(data)
    return result.hexdigest()


def database_counts(connection: sqlite3.Connection) -> dict:
    return {name: connection.execute(f'SELECT count(*) FROM "{name}"').fetchone()[0]
            for name in ("memo", "attachment", "user", "journal_document")}


def backup(source: Path, output: Path, database: str, server_stopped: bool) -> dict:
    source, output = source.resolve(), output.resolve()
    if not server_stopped:
        raise ValueError("先停止实例，再传 --server-stopped；本工具不对运行中的数据库和媒体做不一致的文件复制")
    if not source.is_dir() or output.is_relative_to(source) or output.exists():
        raise ValueError("源目录须存在；输出须在源目录之外且不能覆盖已有文件")
    if Path(database).name != database or not (source / database).is_file():
        raise ValueError("请指定源目录内已有的 SQLite 数据库文件名")
    output.parent.mkdir(parents=True, exist_ok=True)
    report = {"format": FORMAT, "createdTs": int(time.time()), "database": database,
              "scope": "private-instance", "externalDependencies": [], "files": {}}
    temporary = output.with_name(output.name + ".partial-" + secrets.token_hex(6))
    try:
        with tempfile.TemporaryDirectory(prefix="journal-backup-") as work:
            snapshot = Path(work) / database
            with closing(sqlite3.connect((source / database).as_uri() + "?mode=ro", uri=True)) as origin:
                with closing(sqlite3.connect(snapshot)) as copied:
                    origin.backup(copied)
                    if copied.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
                        raise ValueError("数据库完整性检查失败")
                    report["counts"] = database_counts(copied)
                    report["externalDependencies"] = [dict(uid=row[0], storage=row[1]) for row in copied.execute(
                        "SELECT uid, storage_type FROM attachment WHERE storage_type IN ('S3','EXTERNAL')")]
            with temporary.open("xb") as raw:
                os.chmod(temporary, 0o600)
                with zipfile.ZipFile(raw, "w", compression=zipfile.ZIP_DEFLATED, allowZip64=True) as archive:
                    candidates = [(snapshot, database)]
                    for path in sorted(source.rglob("*")):
                        relative = path.relative_to(source).as_posix()
                        if path.is_symlink():
                            raise ValueError("数据目录含符号链接；请先确认其独立备份范围")
                        if path.is_file() and relative not in (database, database + "-wal", database + "-shm"):
                            candidates.append((path, relative))
                    total = 0
                    for path, relative in candidates:
                        total += path.stat().st_size
                        if total > MAX_BYTES or len(report["files"]) >= MAX_ENTRIES:
                            raise ValueError("实例超过本工具 32 GiB / 100000 文件限制")
                        name = "data/" + relative
                        report["files"][name] = {"bytes": path.stat().st_size, "sha256": digest(path)}
                        archive.write(path, name)
                        if digest(path) != report["files"][name]["sha256"]:
                            raise ValueError("源文件在备份期间变化；请停止实例后重试")
                    archive.writestr("manifest.json", json.dumps(report, ensure_ascii=False, indent=2))
                raw.flush()
                os.fsync(raw.fileno())
        os.replace(temporary, output)
        return report
    finally:
        temporary.unlink(missing_ok=True)


def pause_restored_permissions(connection: sqlite3.Connection) -> dict:
    """The copy retains history but cannot revive bearer access or pending sends."""
    removed_native = connection.execute("SELECT count(*) FROM memo_share").fetchone()[0]
    connection.execute("DELETE FROM memo_share")
    connection.execute("DELETE FROM user_setting WHERE key IN ('REFRESH_TOKENS','PERSONAL_ACCESS_TOKENS','WEBHOOKS')")
    rows = list(connection.execute("SELECT owner_id,kind,document_key,payload FROM journal_document"))
    changed = 0
    for owner, kind, key, raw in rows:
        data = json.loads(raw)
        if kind == "share":
            data.update(paused=True, token=secrets.token_hex(32))
        elif kind == "partition-target":
            data.update(enabled=False, url="", signingSecret="", hasSigningSecret=False, epoch=data.get("epoch", 0) + 1)
        elif kind == "partition-mapping":
            data["suspended"] = True
        elif kind == "partition-delivery":
            if data.get("status") not in ("published", "delivered", "retracted"):
                data.update(status="cancelled", lastError="从备份恢复；旧任务不重放")
            data.update(nextAttemptTs=0, leaseUntilTs=0)
        else:
            continue
        connection.execute("UPDATE journal_document SET payload=?,version=version+1 WHERE owner_id=? AND kind=? AND document_key=?",
                           (json.dumps(data, ensure_ascii=False), owner, kind, key))
        changed += 1
    basic = connection.execute("SELECT value FROM system_setting WHERE name='BASIC'").fetchone()
    if basic:
        value = json.loads(basic[0]); value["secretKey"] = secrets.token_hex(32)
        connection.execute("UPDATE system_setting SET value=? WHERE name='BASIC'", (json.dumps(value),))
    ai = connection.execute("SELECT value FROM system_setting WHERE name='AI'").fetchone()
    if ai:
        value = json.loads(ai[0])
        for provider in value.get("providers", []):
            provider.pop("apiKey", None)
        connection.execute("UPDATE system_setting SET value=? WHERE name='AI'", (json.dumps(value),))
    return {"removedNativeShareLinks": removed_native, "pausedDocuments": changed,
            "sessionsAndAccessTokensRevoked": True, "externalSendingPaused": True}


def restore(archive_path: Path, destination: Path) -> dict:
    destination = destination.resolve()
    if destination.exists():
        raise ValueError("恢复只允许写入全新目录，不能覆盖现有实例")
    destination.parent.mkdir(parents=True, exist_ok=True)
    stage = Path(tempfile.mkdtemp(prefix=".journal-restore-", dir=destination.parent))
    try:
        with zipfile.ZipFile(archive_path) as archive:
            entries = archive.infolist()
            names = [entry.filename for entry in entries]
            if len(entries) > MAX_ENTRIES + 1 or len(names) != len(set(names)) or sum(e.file_size for e in entries) > MAX_BYTES:
                raise ValueError("备份重复文件或超过解压限制")
            if "manifest.json" not in names or archive.getinfo("manifest.json").file_size > 32 * 1024**2:
                raise ValueError("备份清单缺失或过大")
            manifest = json.loads(archive.read("manifest.json"))
            if manifest.get("format") != FORMAT or set(names) != set(manifest.get("files", {})) | {"manifest.json"}:
                raise ValueError("备份格式或文件清单不匹配")
            for entry in entries:
                if entry.filename == "manifest.json":
                    continue
                parts = PurePosixPath(entry.filename).parts
                if (len(parts) < 2 or parts[0] != "data" or any(part in ("..", "") for part in parts)
                        or "\\" in entry.filename or (entry.external_attr >> 16) & 0o170000 == 0o120000):
                    raise ValueError("备份包含不安全路径或符号链接")
                target = stage.joinpath(*parts[1:])
                target.parent.mkdir(parents=True, exist_ok=True)
                with archive.open(entry) as source, target.open("xb") as output:
                    shutil.copyfileobj(source, output, 1024 * 1024)
                os.chmod(target, 0o600)
                expected = manifest["files"][entry.filename]
                if target.stat().st_size != expected["bytes"] or digest(target) != expected["sha256"]:
                    raise ValueError("备份校验和不匹配：" + entry.filename)
            database = manifest["database"]
            if Path(database).name != database:
                raise ValueError("数据库路径无效")
            with closing(sqlite3.connect(stage / database)) as connection:
                if connection.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
                    raise ValueError("恢复数据库完整性检查失败")
                if database_counts(connection) != manifest["counts"]:
                    raise ValueError("恢复条数不匹配")
                safety = pause_restored_permissions(connection)
                connection.commit()
            report = {"format": FORMAT, "restoredTs": int(time.time()), "counts": manifest["counts"],
                      "externalDependencies": manifest["externalDependencies"], **safety}
            (stage / "restore-report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2))
        os.replace(stage, destination)
        return report
    finally:
        if stage.exists():
            shutil.rmtree(stage)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="action", required=True)
    create = sub.add_parser("backup")
    create.add_argument("--source", type=Path, required=True)
    create.add_argument("--output", type=Path, required=True)
    create.add_argument("--database", default="memos_prod.db")
    create.add_argument("--server-stopped", action="store_true")
    recover = sub.add_parser("restore")
    recover.add_argument("--archive", type=Path, required=True)
    recover.add_argument("--destination", type=Path, required=True)
    args = parser.parse_args()
    try:
        result = backup(args.source, args.output, args.database, args.server_stopped) if args.action == "backup" else restore(args.archive, args.destination)
        # Do not print credentials or record contents from a private archive.
        print(json.dumps({key: value for key, value in result.items() if key not in ("files",)}, ensure_ascii=False, indent=2))
    except (ValueError, OSError, sqlite3.Error, zipfile.BadZipFile, KeyError) as failure:
        parser.exit(1, "未完成：" + str(failure) + "\n")


if __name__ == "__main__":
    main()
