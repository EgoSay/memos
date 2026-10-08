import importlib.util
from contextlib import closing
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

MODULE = Path(__file__).resolve().parents[1] / "journal_snapshot.py"
spec = importlib.util.spec_from_file_location("snapshot", MODULE)
snapshot = importlib.util.module_from_spec(spec)
spec.loader.exec_module(snapshot)


class OnlineSnapshotTest(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.TemporaryDirectory()
        self.addCleanup(self.work.cleanup)
        self.root = Path(self.work.name).resolve()
        self.source = self.root / "source"
        self.source.mkdir()
        self.db = sqlite3.connect(self.source / snapshot.DATABASE)
        self.addCleanup(self.db.close)
        self.db.execute("PRAGMA journal_mode=WAL")
        self.db.executescript((MODULE.parent.parent / "store/migration/sqlite/LATEST.sql").read_text())
        self.db.execute("INSERT INTO user(username,password_hash) VALUES('owner','hash')")
        self.db.execute("INSERT INTO memo(uid,creator_id,content) VALUES('entry',1,'原话')")
        self.db.execute("INSERT INTO attachment(uid,creator_id,storage_type,reference) VALUES('photo',1,'LOCAL','/var/opt/memos/assets/photo.webp')")
        revision = {"memo": {"attachments": [{"name": "attachments/old"}]}}
        self.db.execute("INSERT INTO journal_document VALUES(1,'revision','entry_old',?,1,0)", (json.dumps(revision),))
        self.db.commit()
        for name, body in {"assets/photo.webp": b"served picture", "originals/photo": b"original picture", "originals/old": b"historical voice"}.items():
            path = self.source / name
            path.parent.mkdir(exist_ok=True)
            path.write_bytes(body)

    def create(self, name="snapshot"):
        return snapshot.snapshot(self.source, self.root / name, Path("/var/opt/memos"))

    def test_running_wal_database_and_historical_media_survive_source_deletion(self):
        report = self.create()
        self.assertEqual(report["counts"]["memo"], 1)
        self.db.execute("UPDATE memo SET content='newer'")
        self.db.commit()
        (self.source / "originals/old").unlink()
        snapshot.verify(self.root / "snapshot")
        with closing(sqlite3.connect(self.root / "snapshot/data/memos_prod.db")) as copy:
            self.assertEqual(copy.execute("SELECT content FROM memo").fetchone()[0], "原话")
        self.assertEqual((self.root / "snapshot/data/originals/old").read_bytes(), b"historical voice")

    def test_concurrent_deletion_fails_without_publishing_partial_snapshot(self):
        original = snapshot.retain_file

        def delete_before_pin(source, target):
            if source.name == "old":
                source.unlink()
            original(source, target)

        with patch.object(snapshot, "retain_file", delete_before_pin):
            with self.assertRaises((ValueError, OSError)):
                self.create()
        self.assertFalse((self.root / "snapshot").exists())
        self.assertFalse(list(self.root.glob(".journal-snapshot-*")))

    def test_changed_pinned_file_cannot_pass_verification(self):
        self.create()
        (self.source / "originals/photo").write_bytes(b"unexpected mutation")
        with self.assertRaises(ValueError):
            snapshot.verify(self.root / "snapshot")

    def test_remote_attachment_and_missing_original_are_not_reported_complete(self):
        self.db.execute("UPDATE attachment SET storage_type='S3'")
        self.db.commit()
        with self.assertRaises(ValueError):
            self.create()
        self.db.execute("UPDATE attachment SET storage_type='LOCAL'")
        self.db.commit()
        (self.source / "originals/photo").unlink()
        with self.assertRaises(ValueError):
            self.create()

    def test_unsafe_reference_and_symlink_fail_closed(self):
        self.db.execute("UPDATE attachment SET reference='../outside'")
        self.db.commit()
        with self.assertRaises(ValueError):
            self.create()
        self.db.execute("UPDATE attachment SET reference='assets/photo.webp'")
        self.db.commit()
        path = self.source / "assets/photo.webp"
        path.unlink()
        path.symlink_to(self.source / "originals/photo")
        with self.assertRaises(ValueError):
            self.create()

    def test_manifest_omitting_required_original_fails(self):
        self.create()
        path = self.root / "snapshot/manifest.json"
        manifest = json.loads(path.read_text())
        del manifest["files"]["data/originals/old"]
        path.write_text(json.dumps(manifest))
        with self.assertRaises(ValueError):
            snapshot.verify(self.root / "snapshot")

    def test_manifest_must_checksum_the_database(self):
        self.create()
        path = self.root / "snapshot/manifest.json"
        manifest = json.loads(path.read_text())
        del manifest["files"]["data/memos_prod.db"]
        path.write_text(json.dumps(manifest))
        with self.assertRaises(ValueError):
            snapshot.verify(self.root / "snapshot")


if __name__ == "__main__":
    unittest.main()
