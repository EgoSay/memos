import importlib.util
from contextlib import closing
import json
from pathlib import Path
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SCRIPTS = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(SCRIPTS))
spec = importlib.util.spec_from_file_location("cloud_backup", SCRIPTS / "journal_cloud_backup.py")
backup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)


class CloudBackupTest(unittest.TestCase):
    def setUp(self):
        work = tempfile.TemporaryDirectory()
        self.addCleanup(work.cleanup)
        self.root = Path(work.name).resolve()
        self.source = self.root / "source"
        self.source.mkdir()
        with closing(sqlite3.connect(self.source / "memos_prod.db")) as db, db:
            db.executescript((SCRIPTS.parent / "store/migration/sqlite/LATEST.sql").read_text())
            db.execute("INSERT INTO user(username,password_hash) VALUES('owner','hash')")
            db.execute("INSERT INTO memo(uid,creator_id,content) VALUES('entry',1,'a memory')")
        self.work = self.root / "work"
        self.work.mkdir()
        self.previous = {"status": "success", "snapshotId": "previous", "sourceTs": 1}
        backup.atomic_json(self.work / "last-success.json", self.previous)
        self.config = {"source": str(self.source), "logicalRoot": "/var/opt/memos",
                       "workDir": str(self.work), "repository": "s3:example.invalid/private",
                       "passwordFile": str(self.root / "password"), "host": "test"}

    def receipt(self, name):
        return json.loads((self.work / name).read_text())

    @staticmethod
    def remote_success(config, *arguments, **kwargs):
        if arguments[0] == "backup":
            return json.dumps({"message_type": "summary", "snapshot_id": "new-snapshot"})
        return json.dumps([{"id": "new-snapshot"}])

    def test_partial_restic_snapshot_does_not_advance_last_success(self):
        partial = subprocess.CompletedProcess([], 3, stdout='{"snapshot_id":"partial"}', stderr="private path")
        with patch.object(backup.subprocess, "run", return_value=partial):
            with self.assertRaisesRegex(RuntimeError, "exit 3"):
                backup.run(self.config)
        self.assertEqual(self.receipt("last-success.json"), self.previous)
        self.assertEqual(self.receipt("last-attempt.json")["status"], "failed")
        self.assertFalse((self.work / "current").exists())

    def test_unconfirmed_remote_snapshot_is_not_success(self):
        def empty_listing(config, *arguments, **kwargs):
            return "[]" if arguments[0] == "snapshots" else self.remote_success(config, *arguments, **kwargs)
        with patch.object(backup, "restic", side_effect=empty_listing):
            with self.assertRaisesRegex(RuntimeError, "not confirmed"):
                backup.run(self.config)
        self.assertEqual(self.receipt("last-success.json"), self.previous)

    def test_corrupted_staging_database_after_upload_is_not_success(self):
        def changed_file(config, *arguments, **kwargs):
            if arguments[0] == "backup":
                with closing(sqlite3.connect(kwargs["cwd"] / "data/memos_prod.db")) as db, db:
                    db.execute("UPDATE memo SET content='changed during upload'")
            return self.remote_success(config, *arguments, **kwargs)
        with patch.object(backup, "restic", side_effect=changed_file):
            with self.assertRaises(ValueError):
                backup.run(self.config)
        self.assertEqual(self.receipt("last-success.json"), self.previous)

    def test_success_records_verified_snapshot_and_removes_staging(self):
        with patch.object(backup, "restic", side_effect=self.remote_success):
            result = backup.run(self.config)
        self.assertEqual(result["snapshotId"], "new-snapshot")
        self.assertTrue(result["mediaComplete"])
        self.assertEqual(self.receipt("last-success.json"), result)
        self.assertFalse((self.work / "current").exists())

    def test_monitor_failure_keeps_valid_backup_and_reports_delivery_failure(self):
        self.config.update(reportUrl="https://example.invalid/receipt", reportToken="test-token")
        with patch.object(backup, "restic", side_effect=self.remote_success), \
                patch.object(backup, "publish_receipt", side_effect=TimeoutError):
            with self.assertRaises(TimeoutError):
                backup.run(self.config)
        self.assertEqual(self.receipt("last-success.json")["snapshotId"], "new-snapshot")
        self.assertEqual(self.receipt("last-attempt.json")["status"], "success")
        self.assertEqual(self.receipt("last-report.json")["status"], "failed")

    def test_overlapping_backup_does_not_write_receipts(self):
        with (self.work / "lock").open("a") as lock:
            backup.fcntl.flock(lock, backup.fcntl.LOCK_EX | backup.fcntl.LOCK_NB)
            result = backup.run(self.config)
        self.assertEqual(result["status"], "skipped")
        self.assertEqual(self.receipt("last-success.json"), self.previous)
        self.assertFalse((self.work / "last-attempt.json").exists())


if __name__ == "__main__":
    unittest.main()
