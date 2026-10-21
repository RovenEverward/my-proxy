"""部署失败路径：只用临时 SQLite 和假的 systemctl，不启动服务。"""
from contextlib import closing
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('remote', Path(__file__).with_name('dev-deploy-remote.py'))
remote = importlib.util.module_from_spec(spec)
spec.loader.exec_module(remote)


class TransactionTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.db = self.root / 'mpx.db'
        with closing(sqlite3.connect(self.db)) as db, db:
            db.execute('create table value (n integer)')
            db.execute('insert into value values(1)')
        old = self.root / 'releases/old/mpx'
        old.parent.mkdir(parents=True)
        old.write_bytes(b'old executable')
        self.link = self.root / 'mpx'
        self.link.symlink_to(old)
        env = self.root / 'env'
        env.write_text('MPX_TOKEN=test-only-token')
        cfg = dict(db=str(self.db), binary=str(self.link), real_binary=str(old), env_file=str(env), addr='127.0.0.1:8080', service='test.service')
        state = patch.object(remote, 'STATE_ROOT', self.root / 'trusted')
        state.start()
        self.addCleanup(state.stop)
        self.tx = remote.Transaction(cfg)
        self.tx.system = lambda *args, **kwargs: False if args[0] == 'is-active' else True
        self.tx.status = lambda: {'version': 'old'}
        self.tx.api = lambda path: b'{}'
        self.tx.nodes = lambda: {}
        self.tx.exports = lambda: {'1:base64-links': 'eA=='}
        self.tx.health = lambda version: {'version': version}
        self.addCleanup(self.tx.lock.close)

    def test_disconnect_restores_database_and_binary(self):
        saved = self.tx.save()
        self.assertTrue(Path(saved['backup'], 'backup.tar.gz').is_file())
        with closing(sqlite3.connect(self.db)) as db, db:
            db.execute('update value set n=2')
        other = self.root / 'new'
        other.write_bytes(b'new executable')
        self.tx.switch(other)
        self.tx.close()  # EOF 或任何确认前异常走相同路径。
        self.assertEqual(self.link.read_bytes(), b'old executable')
        with closing(sqlite3.connect(self.db)) as db, db:
            self.assertEqual(db.execute('select n from value').fetchone()[0], 1)
        self.assertFalse((self.tx.root / 'pending.json').exists())

    def test_commit_keeps_current_and_records_backup(self):
        saved = self.tx.save()
        self.tx.finish()
        self.tx.close()
        self.assertEqual(json.loads((self.tx.root / 'latest.json').read_text())['backup'], saved['backup'])

    def test_pending_transaction_refuses_new_backup(self):
        (self.tx.root / 'pending.json').write_text('{}')
        with self.assertRaisesRegex(RuntimeError, 'unfinished'):
            self.tx.save()

    def test_bad_backup_checksum_stops_restore(self):
        saved = self.tx.save()
        Path(saved['backup'], 'mpx').write_bytes(b'corrupt')
        with self.assertRaisesRegex(RuntimeError, 'checksum'):
            self.tx.restore(saved['backup'])

    def test_repeated_rollback_does_not_reinstall_development(self):
        first = self.tx.save()
        self.tx.finish()
        self.link.write_bytes(b'development executable')
        for _ in range(2):
            self.tx.saved = None
            self.tx.committed = False
            self.tx.save()
            self.tx.activate({'rollback': True})
            self.tx.finish()
            self.tx.close()
            self.assertEqual(self.link.read_bytes(), b'old executable')
            self.assertEqual(json.loads((self.tx.root / 'latest.json').read_text())['backup'], first['backup'])

    def test_untrusted_state_symlink_is_rejected(self):
        link = self.root / 'untrusted'
        link.symlink_to(self.tx.root)
        with patch.object(remote, 'STATE_ROOT', link):
            with self.assertRaisesRegex(RuntimeError, 'untrusted-state'):
                remote.Transaction(self.tx.cfg)

    def test_api_retries_rate_limit_without_printing_credentials(self):
        from unittest.mock import MagicMock
        import urllib.error
        opener = MagicMock()
        response = MagicMock()
        response.__enter__.return_value.read.return_value = b'{}'
        opener.open.side_effect = [urllib.error.HTTPError('http://127.0.0.1', 429, 'limited', {'Retry-After': '1'}, None), response]
        with patch.object(remote.urllib.request, 'build_opener', return_value=opener), patch.object(remote.time, 'sleep'):
            self.assertEqual(remote.Transaction.api(self.tx, '/api/status'), b'{}')
        self.assertEqual(opener.open.call_count, 2)

    def test_specific_ipv6_listener_is_preserved(self):
        cfg = {**self.tx.cfg, 'addr': '[::1]:8080'}
        self.tx.lock.close()
        other = remote.Transaction(cfg)
        self.addCleanup(other.lock.close)
        self.assertEqual(other.base, 'http://[::1]:8080')
