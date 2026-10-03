import importlib.util
from contextlib import closing
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
import zipfile

MODULE = Path(__file__).resolve().parents[1] / "journal_backup.py"
spec = importlib.util.spec_from_file_location("journal_backup", MODULE)
backup = importlib.util.module_from_spec(spec)
spec.loader.exec_module(backup)

class JournalBackupTest(unittest.TestCase):
    def setUp(self):
        self.work = tempfile.TemporaryDirectory()
        self.addCleanup(self.work.cleanup)
        self.root = Path(self.work.name)
        self.source = self.root / "source"
        self.source.mkdir()
        schema = MODULE.parent.parent / "store/migration/sqlite/LATEST.sql"
        with closing(sqlite3.connect(self.source / "memos_prod.db")) as db, db:
            db.executescript(schema.read_text())
            db.execute("INSERT INTO user(id,username,password_hash) VALUES(1,'owner','retained-hash')")
            db.execute("INSERT INTO memo(uid,creator_id,content,created_ts) VALUES('moment',1,'原话不改',12345)")
            db.execute("INSERT INTO attachment(uid,creator_id,filename,type,size,blob) VALUES('voice',1,'voice.webm','audio/webm',3,?)", (b'\x00\x01\x02',))
            for kind, payload in (("share", {"token":"old-token","paused":False}), ("partition-target", {"enabled":True,"epoch":1,"signingSecret":"secret","url":"https://example.test/key"}), ("partition-delivery", {"status":"pending","eventId":"stable-id"})):
                db.execute("INSERT INTO journal_document VALUES(1,?,'fixture',?,1,0)", (kind,json.dumps(payload)))
        (self.source / "originals").mkdir()
        (self.source / "originals/voice").write_bytes(b'\x00\x01\x02')

    def test_roundtrip_retains_database_and_originals_but_revokes_external_access(self):
        archive = self.root / "independent.zip"
        manifest = backup.backup(self.source, archive, "memos_prod.db", True)
        self.assertEqual(manifest['counts']['memo'],1)
        output = self.root / "fresh"
        result = backup.restore(archive, output)
        self.assertTrue(result['externalSendingPaused'])
        self.assertEqual((output / "originals/voice").read_bytes(),b'\x00\x01\x02')
        with closing(sqlite3.connect(output / "memos_prod.db")) as db, db:
            self.assertEqual(db.execute("SELECT content,created_ts FROM memo").fetchone(),('原话不改',12345))
            values = {kind:json.loads(raw) for kind,raw in db.execute("SELECT kind,payload FROM journal_document")}
            self.assertTrue(values['share']['paused'])
            self.assertNotEqual(values['share']['token'],'old-token')
            self.assertFalse(values['partition-target']['enabled'])
            self.assertEqual(values['partition-target']['signingSecret'],'')
            self.assertEqual(values['partition-delivery']['status'],'cancelled')
            self.assertEqual(values['partition-delivery']['eventId'],'stable-id')
        with self.assertRaises(ValueError): backup.restore(archive, output)

    def test_unsafe_paths_corruption_and_live_copy_fail_closed(self):
        archive = self.root / "archive.zip"
        with self.assertRaises(ValueError): backup.backup(self.source, archive, "memos_prod.db", False)
        backup.backup(self.source, archive, "memos_prod.db", True)
        corrupt = self.root / "corrupt.zip"
        with zipfile.ZipFile(archive) as original, zipfile.ZipFile(corrupt,'w') as out:
            for name in original.namelist():
                out.writestr(name,b'changed' if name == 'data/originals/voice' else original.read(name))
        target = self.root / "corrupt-target"
        with self.assertRaises(ValueError): backup.restore(corrupt,target)
        self.assertFalse(target.exists())
        with zipfile.ZipFile(corrupt,'w') as out:
            out.writestr('manifest.json',json.dumps({'format':backup.FORMAT,'files':{'data/../../escape':{}},'database':'memos_prod.db'}))
            out.writestr('data/../../escape','bad')
        with self.assertRaises(ValueError): backup.restore(corrupt,target)
        self.assertFalse((self.root/'escape').exists())

if __name__ == '__main__': unittest.main()
