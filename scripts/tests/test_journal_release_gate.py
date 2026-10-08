import importlib.util,pathlib,sqlite3,tempfile,sys,unittest
from contextlib import closing
SCRIPTS=pathlib.Path(__file__).resolve().parents[1];sys.path.insert(0,str(SCRIPTS))
spec=importlib.util.spec_from_file_location('gate',SCRIPTS/'journal_release_gate.py');gate=importlib.util.module_from_spec(spec);spec.loader.exec_module(gate)
class ReleaseGateTest(unittest.TestCase):
 def test_only_fixed_registry_content_identifiers_are_accepted(self):
  good={'commit':'a'*40,'digest':'sha256:'+'b'*64};self.assertEqual(gate.validate(good),good)
  for value in ({**good,'commit':'main'}, {**good,'digest':'sha256:abc; echo bad'}, {**good,'command':'anything'}):
   with self.assertRaises(ValueError):gate.validate(value)
 def test_new_content_or_a_changed_data_shape_blocks_snapshot_replacement(self):
  with tempfile.TemporaryDirectory() as directory:
   p=pathlib.Path(directory)/'memos_prod.db'
   with closing(sqlite3.connect(p)) as db,db:
    for table in ('memo','attachment','journal_document'):db.execute('CREATE TABLE '+table+'(id INTEGER, content TEXT)')
    db.execute("INSERT INTO memo VALUES(1,'before')")
   first=gate.fingerprint(p)
   with closing(sqlite3.connect(p)) as db,db:db.execute("UPDATE memo SET content='new memory'")
   self.assertNotEqual(first,gate.fingerprint(p))
   with closing(sqlite3.connect(p)) as db,db:db.execute('ALTER TABLE memo ADD COLUMN extra TEXT')
   self.assertNotEqual(first,gate.fingerprint(p))

class InterruptedPreparationTest(unittest.TestCase):
 def test_unfinished_backup_does_not_trigger_data_replacement(self):
  import json
  from unittest.mock import patch
  with tempfile.TemporaryDirectory() as directory:
   root=pathlib.Path(directory)
   (root/'pending.json').write_text(json.dumps({'status':'preparing','previousImage':'previous-image'}))
   with patch.object(gate,'ROOT',root),patch.object(gate,'command',return_value='') as command:
    self.assertEqual(gate.rollback()['status'],'preparation-aborted')
   self.assertFalse((root/'pending.json').exists())
   command.assert_any_call('docker','start',gate.CONTAINER)
   command.assert_any_call('systemctl','start','memos-backup.timer')

class BackupOverlapTest(unittest.TestCase):
 def test_oneshot_activating_and_stopping_states_are_not_treated_as_finished(self):
  from unittest.mock import patch
  with patch.object(gate,'command',side_effect=['activating\n','deactivating\n','inactive\n']),patch.object(gate.time,'sleep') as sleep:
   gate.wait_for_backup()
  self.assertEqual(sleep.call_count,2)

class RollbackPreservationTest(unittest.TestCase):
 def test_new_content_and_unreadable_schemas_restart_service_without_replacing_data(self):
  import json
  from unittest.mock import patch
  for unreadable in (False,True):
   with self.subTest(unreadable=unreadable),tempfile.TemporaryDirectory() as directory:
    root=pathlib.Path(directory);source=root/'live';source.mkdir();database=source/'memos_prod.db'
    with closing(sqlite3.connect(database)) as db,db:
     for table in ('memo','attachment','journal_document'):db.execute('CREATE TABLE '+table+'(id INTEGER, content TEXT)')
     db.execute("INSERT INTO memo VALUES(1,'before')")
    before=gate.fingerprint(database)
    with closing(sqlite3.connect(database)) as db,db:
     db.execute("UPDATE memo SET content='a new memory'")
     if unreadable:db.execute('DROP TABLE attachment')
    (root/'pending.json').write_text(json.dumps({'status':'prepared','source':str(source),'stage':str(root/'snapshot'),'fingerprint':before}))
    with patch.object(gate,'ROOT',root),patch.object(gate,'verify'),patch.object(gate,'command',return_value='') as command:
     with self.assertRaisesRegex(RuntimeError,'preserved'):gate.rollback()
    with closing(sqlite3.connect(database)) as db:self.assertEqual(db.execute('SELECT content FROM memo').fetchone()[0],'a new memory')
    self.assertTrue((root/'pending.json').exists())
    command.assert_any_call('docker','start',gate.CONTAINER)
    command.assert_any_call('systemctl','start','memos-backup.timer')

if __name__=='__main__':unittest.main()
