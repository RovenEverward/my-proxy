"""经已校验 SSH 通道执行的部署事务；标准输出仅供本机协议读取。"""
from contextlib import closing
import base64
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import signal
import sqlite3
import subprocess
import sys
import tarfile
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone


STATE_ROOT = Path("/var/lib/mpx-dev-deploy")


def state_directory():
    parent = STATE_ROOT.parent
    info = parent.lstat()
    if parent.is_symlink() or info.st_uid != os.geteuid() or info.st_mode & 0o022:
        raise RuntimeError("untrusted-state-parent")
    STATE_ROOT.mkdir(mode=0o700, exist_ok=True)
    info = STATE_ROOT.lstat()
    if STATE_ROOT.is_symlink() or not STATE_ROOT.is_dir() or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise RuntimeError("untrusted-state-directory")
    return STATE_ROOT


class Transaction:
    def __init__(self, cfg):
        self.cfg = cfg
        self.root = state_directory()
        self.lock = (self.root / 'lock').open('a')
        fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        self.saved = None
        self.stopped = False
        self.committed = False
        self.rollback = False
        self.watcher = False
        self.token = None
        for line in Path(cfg['env_file']).read_text().splitlines():
            if line.startswith('MPX_TOKEN='):
                self.token = line.split('=', 1)[1].strip().strip('\"\'')
        if not self.token:
            raise RuntimeError('missing-token')
        host, port = cfg['addr'].rsplit(':', 1)
        host = host.strip('[]')
        if host in ('', '0.0.0.0'):
            host = '127.0.0.1'
        elif host == '::':
            host = '::1'
        authority = '[' + host + ']' if ':' in host else host
        self.base = 'http://' + authority + ':' + str(int(port))
        self.last_request = 0

    def system(self, *args, check=True):
        result = subprocess.run(['systemctl', *args], capture_output=True)
        if check and result.returncode:
            raise RuntimeError('systemctl-failed')
        return result.returncode == 0

    def api(self, path):
        req = urllib.request.Request(self.base + path, headers={'Authorization': 'Bearer ' + self.token})
        # 不经过代理，不跟随订阅中的外部主机地址。
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        # 三种订阅共享每秒 10 次的桶，主动留出余量；管理请求也走同一节奏。
        for attempt in range(6):
            delay = 0.15 - (time.monotonic() - self.last_request)
            if delay > 0:
                time.sleep(delay)
            self.last_request = time.monotonic()
            try:
                with opener.open(req, timeout=8) as response:
                    return response.read()
            except urllib.error.HTTPError as error:
                if error.code != 429 or attempt == 5:
                    raise RuntimeError('api-request-failed') from None
                try:
                    delay = max(1, min(30, int(error.headers.get('Retry-After', '1'))))
                except ValueError:
                    delay = 1
                error.close()
                time.sleep(delay)
        raise RuntimeError('api-rate-limited')

    def status(self):
        return json.loads(self.api('/api/status'))

    def exports(self):
        users, offset = [], 0
        while True:
            page = json.loads(self.api('/api/users?limit=1000&offset=' + str(offset)))
            users.extend(page['users'])
            offset += len(page['users'])
            if offset >= page['total']:
                break
            if not page['users']:
                raise RuntimeError('incomplete-users')
        result = {}
        for user in users:
            name = user['username']
            links = json.loads(self.api('/api/users/' + urllib.parse.quote(name, safe='') + '/links'))
            # token 是唯一路由凭据；地址由本机固定生成，绝不访问链接中的主机。
            for fmt in ('clash-meta', 'sing-box', 'base64-links'):
                link = links.get('formats', {}).get(fmt) or links.get(fmt)
                if isinstance(link, dict):
                    link = link.get('url')
                if not link:
                    raise RuntimeError('missing-subscription-format')
                parts = urllib.parse.urlsplit(link).path.split('/')
                token = parts[parts.index('sub') + 1]
                data = self.api('/sub/' + urllib.parse.quote(token, safe='') + '/' + fmt)
                result[str(user['id']) + ':' + fmt] = base64.b64encode(data).decode()
        return result

    def health(self, version):
        for _ in range(30):
            try:
                status = self.status()
                if self.system('is-active', '--quiet', self.cfg['service'], check=False) and status['version'] == version:
                    return status
            except Exception:
                pass
            time.sleep(1)
        raise RuntimeError('health-failed')

    def nodes(self):
        nodes = json.loads(self.api('/api/nodes'))
        return {str(node['id']): {key: node.get(key) for key in ('deployed_at', 'deployed_hash', 'deployed_template_hash')} for node in nodes}

    def save(self):
        if (self.root / 'pending.json').exists():
            raise RuntimeError('unfinished-transaction')
        if Path('/var/lib/mpx/update/request').exists() or self.system('is-active', '--quiet', 'mpx-update.service', check=False):
            raise RuntimeError('release-update-in-progress')
        before = self.status()
        settings = json.loads(self.api('/api/settings'))
        defaults = {fmt: not (settings.get('templates') or {}).get(fmt, '').strip()
                    for fmt in ('clash-meta', 'sing-box', 'base64-links')}
        if Path(self.cfg['db'] + '.restore').exists():
            raise RuntimeError('database-restore-pending')
        exports = self.exports()
        nodes = self.nodes()
        self.watcher = self.system('is-active', '--quiet', 'mpx-update.path', check=False)
        if self.watcher:
            self.system('stop', 'mpx-update.path')
        self.system('stop', self.cfg['service'])
        self.stopped = True
        if Path('/var/lib/mpx/update/request').exists() or self.system('is-active', '--quiet', 'mpx-update.service', check=False):
            raise RuntimeError('release-update-raced')
        folder = self.root / 'backups' / datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
        folder.mkdir(parents=True, mode=0o700)
        db = Path(self.cfg['db'])
        info = db.stat()
        with closing(sqlite3.connect(db)) as source, closing(sqlite3.connect(folder / 'mpx.db')) as target:
            source.backup(target)
        shutil.copy2(Path(self.cfg['binary']).resolve(), folder / 'mpx')
        os.chmod(folder / 'mpx.db', 0o600)
        metadata = {'version': before['version'], 'link': os.readlink(self.cfg['binary']),
                    'uid': info.st_uid, 'gid': info.st_gid, 'mode': info.st_mode & 0o777,
                    'binary_sha256': hashlib.sha256((folder / 'mpx').read_bytes()).hexdigest()}
        (folder / 'metadata.json').write_text(json.dumps(metadata))
        (folder / 'before.json').write_text(json.dumps(exports))
        self.saved = folder
        (self.root / 'pending.json').write_text(json.dumps({'backup': str(folder)}))
        archive = folder / 'backup.tar.gz'
        with tarfile.open(archive, 'w:gz') as tar:
            for name in ('mpx.db', 'mpx', 'metadata.json', 'before.json'):
                tar.add(folder / name, arcname=name)
        os.chmod(archive, 0o600)
        return {'backup': str(folder), 'version': before['version'], 'before': exports, 'defaults': defaults, 'nodes': nodes,
                'archive': base64.b64encode(archive.read_bytes()).decode(),
                'sha256': hashlib.sha256(archive.read_bytes()).hexdigest()}

    def switch(self, executable):
        link = Path(self.cfg['binary'])
        temp = link.with_name(link.name + '.dev-new')
        temp.unlink(missing_ok=True)
        temp.symlink_to(executable)
        temp.replace(link)

    def restore(self, folder):
        folder = Path(folder)
        metadata = json.loads((folder / 'metadata.json').read_text())
        if hashlib.sha256((folder / 'mpx').read_bytes()).hexdigest() != metadata['binary_sha256']:
            raise RuntimeError('backup-checksum-failed')
        self.system('stop', self.cfg['service'])
        db = Path(self.cfg['db'])
        temp = db.with_name(db.name + '.dev-restore')
        shutil.copyfile(folder / 'mpx.db', temp)
        os.chown(temp, metadata['uid'], metadata['gid'])
        os.chmod(temp, metadata['mode'])
        for suffix in ('-wal', '-shm'):
            Path(str(db) + suffix).unlink(missing_ok=True)
        temp.replace(db)
        # 保留原发布目录；备份的二进制是最终回滚来源，不依赖旧目录仍存在。
        executable = folder / 'restored-mpx'
        shutil.copy2(folder / 'mpx', executable)
        os.chmod(executable, 0o755)
        # 服务用户必须能遍历发布目录，不能指向 root 私有备份目录。
        release = Path(self.cfg['real_binary']).parent.parent / ('restore-' + folder.name)
        release.mkdir(mode=0o755, exist_ok=True)
        os.chmod(release, 0o755)
        shutil.copy2(executable, release / 'mpx')
        self.switch(release / 'mpx')
        self.system('start', self.cfg['service'])
        self.health(metadata['version'])
        self.stopped = False
        return metadata['version']

    def activate(self, request):
        if not self.saved:
            raise RuntimeError('backup-required')
        if request.get('rollback'):
            self.rollback = True
            previous = json.loads((self.root / 'latest.json').read_text())['backup']
            version = self.restore(previous)
        else:
            version = request['version']
            release = Path(self.cfg['real_binary']).parent.parent / version
            release.mkdir(mode=0o755, exist_ok=True)
            os.chmod(release, 0o755)
            candidate = release / 'mpx'
            if candidate.exists() and hashlib.sha256(candidate.read_bytes()).hexdigest() != request['sha256']:
                raise RuntimeError('version-collision')
            shutil.copy2(self.root / 'candidate', candidate)
            os.chmod(candidate, 0o755)
            self.switch(candidate)
            self.system('start', self.cfg['service'])
            self.stopped = True  # 直到 commit，异常仍需恢复数据库。
            self.health(version)
        users = json.loads(self.api('/api/users?limit=1'))
        return {'version': version, 'after': self.exports(), 'presence': 'presence' in users, 'nodes': self.nodes()}

    def finish(self):
        if self.watcher:
            self.system('start', 'mpx-update.path')
            self.watcher = False
        if self.saved and not self.rollback:
            (self.root / 'latest.new').write_text(json.dumps({'backup': str(self.saved)}))
            (self.root / 'latest.new').replace(self.root / 'latest.json')
        self.committed = True
        (self.root / 'pending.json').unlink(missing_ok=True)

    def close(self):
        if not self.committed:
            if self.saved:
                self.restore(self.saved)
                (self.root / 'pending.json').unlink(missing_ok=True)
            elif self.stopped:
                self.system('start', self.cfg['service'])
        if self.watcher:
            self.system('start', 'mpx-update.path')


def serve():
    os.umask(0o077)
    tx = None
    try:
        for line in sys.stdin:
            request = json.loads(line)
            action = request['action']
            if action == 'init':
                tx = Transaction(request['config'])
                result = {'ready': True}
            elif action == 'upload':
                data = base64.b64decode(request['binary'], validate=True)
                if hashlib.sha256(data).hexdigest() != request['sha256']:
                    raise RuntimeError('upload-checksum-failed')
                (tx.root / 'candidate').write_bytes(data)
                os.chmod(tx.root / 'candidate', 0o755)
                check = subprocess.run([str(tx.root / 'candidate'), 'version'], capture_output=True)
                if check.returncode or request['version'].encode() not in check.stdout + check.stderr:
                    raise RuntimeError('candidate-version-invalid')
                result = {'uploaded': True}
            elif action == 'save':
                result = tx.save()
            elif action == 'activate':
                result = tx.activate(request)
            elif action == 'commit':
                tx.finish()
                result = {'committed': True}
            else:
                raise RuntimeError('unknown-action')
            print(json.dumps({'ok': result}), flush=True)
    except Exception as error:
        print(json.dumps({'error': type(error).__name__}), flush=True)
    finally:
        if tx:
            try:
                tx.close()
                print(json.dumps({'closed': True, 'rolled_back': not tx.committed}), flush=True)
            except Exception:
                print(json.dumps({'closed': False, 'rollback_failed': True}), flush=True)
                sys.exit(2)


if __name__ == '__main__':
    def interrupt(*_):
        raise RuntimeError('terminated')
    signal.signal(signal.SIGTERM, interrupt)
    signal.signal(signal.SIGHUP, interrupt)
    serve()
