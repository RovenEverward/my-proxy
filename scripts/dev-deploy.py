#!/usr/bin/env python3
"""只部署当前已提交工作区；连接信息仅从本机私有配置读取。"""
import argparse
import base64
import difflib
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tarfile
import tempfile

REPO = Path(__file__).resolve().parent.parent
CONFIG = Path.home() / '.config/mpx-dev/deploy.json'


def clean_head():
    if subprocess.check_output(['git', 'status', '--porcelain', '--untracked-files=normal'], cwd=REPO):
        raise RuntimeError('工作区有未提交改动，请先提交，再运行 mpx-dev deploy。')
    return subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=REPO, text=True).strip()


class Remote:
    def __init__(self, config, key):
        known = CONFIG.parent / 'known_hosts'
        if not known.is_file():
            raise RuntimeError('缺少已核实的 SSH 主机指纹，请先配置 known_hosts。')
        source = (REPO / 'scripts/dev-deploy-remote.py').read_text()
        command = ['ssh', '-i', str(key), '-p', str(int(config['ssh_port'])),
                   '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes',
                   '-o', 'ConnectTimeout=15', '-o', 'ServerAliveInterval=10', '-o', 'ServerAliveCountMax=3',
                   '-o', 'UserKnownHostsFile=' + str(known),
                   config['ssh_user'] + '@' + config['ssh_host'], 'python3 -u -c ' + shlex.quote(source)]
        self.error = tempfile.TemporaryFile()
        self.process = subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.error, text=True)
        self.call('init', config={k: config[k] for k in ('service', 'binary', 'real_binary', 'db', 'addr', 'env_file')})

    def call(self, action, **args):
        self.process.stdin.write(json.dumps({'action': action, **args}) + '\n')
        self.process.stdin.flush()
        line = self.process.stdout.readline()
        if not line:
            raise RuntimeError('SSH 部署通道中断；请核对远端回滚状态。')
        result = json.loads(line)
        if 'error' in result:
            raise RuntimeError('远端操作失败（详细输出因可能包含凭据而隐藏）。')
        return result['ok']

    def close(self):
        self.process.stdin.close()
        closed = [json.loads(line) for line in self.process.stdout if line.strip()]
        code = self.process.wait()
        self.error.close()
        if code or not closed or not closed[-1].get('closed'):
            raise RuntimeError('无法确认自动回滚，服务可能已停止；先检查/var/lib/mpx-dev-deploy/pending.json 和备份，禁止继续部署。')
        if closed[-1].get('rolled_back'):
            print('部署未提交，已恢复部署前的数据库与二进制。', file=sys.stderr)


def compile_go(output, package, env=None, flags=()):
    # 编译器报错可包含源码路径，但不会接触本机部署凭据。
    subprocess.run(['go', 'build', '-trimpath', *flags, '-o', str(output), package], cwd=REPO, env=env, check=True)


def compare(before, after, tool, folder, rollback=False, defaults=None):
    if set(before) != set(after):
        raise RuntimeError('部署前后用户或格式集合变化，拒绝确认部署。')
    unexpected = False
    for index, key in enumerate(sorted(before), 1):
        a, b = base64.b64decode(before[key]), base64.b64decode(after[key])
        fmt = key.split(':', 1)[1]
        ap, bp = folder / 'before-sub', folder / 'after-sub'
        ap.write_bytes(a)
        bp.write_bytes(b)
        result = json.loads(subprocess.check_output([str(tool), fmt, str(bp if rollback else ap), str(ap if rollback else bp)]))
        lines = list(difflib.ndiff(a.decode().splitlines(), b.decode().splitlines()))
        added = sum(line.startswith('+ ') for line in lines)
        removed = sum(line.startswith('- ') for line in lines)
        allowed = result['approved'] and (a == b or (defaults or {}).get(fmt, False))
        unexpected |= not allowed
        print(f'订阅 {index}（{fmt}）：' + ('有变化' if a != b else '无变化') +
              f'，增加 {added} 行，删除 {removed} 行，' + ('已批准范围内' if allowed else '超出已批准范围'))
    if unexpected:
        raise RuntimeError('订阅含未批准差异，自动回滚。')


def main():
    parser = argparse.ArgumentParser(description='部署已提交的工作区控制面；失败自动恢复数据库和二进制。')
    parser.add_argument('--rollback', action='store_true', help='恢复上一次部署前的备份')
    args = parser.parse_args()
    os.umask(0o077)
    head = clean_head()
    if CONFIG.stat().st_mode & 0o077:
        raise RuntimeError('deploy.json 必须只允许当前用户读写（chmod 600）。')
    config = json.loads(CONFIG.read_text())
    backup_root = Path.home() / 'mpx-backups'
    backup_root.mkdir(mode=0o700, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='.dev-deploy-', dir=backup_root) as temp:
        folder = Path(temp)
        key = folder / 'key'
        with tarfile.open(config['ssh_key_archive']) as tar:
            member = tar.getmember('id_ed25519')
            if not member.isfile() or member.size > 16384:
                raise RuntimeError('SSH 密钥归档无效。')
            key.write_bytes(tar.extractfile(member).read())
        key.chmod(0o600)
        tool = folder / 'subdiff'
        compile_go(tool, './scripts/dev-subdiff')
        version = 'dev-' + head[:12] + '-clean'
        binary = folder / 'mpx'
        if not args.rollback:
            machine = {'x86_64': 'amd64', 'aarch64': 'arm64'}[config['machine']]
            env = {**os.environ, 'GOOS': 'linux', 'GOARCH': machine, 'CGO_ENABLED': '0'}
            module = next(line.split()[1] for line in (REPO / 'go.mod').read_text().splitlines() if line.startswith('module '))
            print('正在编译控制面 ' + version)
            compile_go(binary, './cmd/mpx', env, ['-ldflags', '-X ' + module + '/internal/buildinfo.version=' + version])
        if clean_head() != head:
            raise RuntimeError('编译期间 HEAD 改变，停止部署。')
        remote = Remote(config, key)
        try:
            if not args.rollback:
                payload = binary.read_bytes()
                checksum = hashlib.sha256(payload).hexdigest()
                remote.call('upload', binary=base64.b64encode(payload).decode(), sha256=checksum, version=version)
            saved = remote.call('save')
            local = backup_root / ('dev-deploy-' + Path(saved['backup']).name)
            local.mkdir(mode=0o700)
            archive = base64.b64decode(saved.pop('archive'), validate=True)
            if hashlib.sha256(archive).hexdigest() != saved['sha256']:
                raise RuntimeError('本机备份校验失败，停止部署。')
            with (local / 'backup.tar.gz').open('wb') as backup:
                backup.write(archive)
                backup.flush()
                os.fsync(backup.fileno())
            (local / 'before.json').write_text(json.dumps(saved['before']))
            print('部署前版本：' + saved['version'])
            print('远端备份：' + saved['backup'])
            print('本机备份：' + str(local))
            active = remote.call('activate', rollback=args.rollback, version=version,
                                 sha256=checksum if not args.rollback else '')
            (local / 'after.json').write_text(json.dumps(active['after']))
            if not args.rollback and saved['nodes'] != active['nodes']:
                raise RuntimeError('节点下发时间或哈希发生变化，自动回滚。')
            compare(saved['before'], active['after'], tool, folder, args.rollback, saved['defaults'])
            if not args.rollback and not active['presence']:
                raise RuntimeError('新控制面缺少 presence，自动回滚。')
            remote.call('commit')
            (local / 'result.json').write_text(json.dumps({'before': saved['version'], 'after': active['version'],
                                                         'presence': active['presence'], 'healthy': True}))
            print('部署后版本：' + active['version'])
            print('健康检查：通过；GET /api/users presence：' + ('有' if active['presence'] else '无'))
        finally:
            remote.close()
    return 0


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (Exception, KeyboardInterrupt) as error:
        # 不输出远端原始错误、HTTP 异常或带主机的命令行。
        message = str(error) if isinstance(error, RuntimeError) else type(error).__name__
        print('mpx-dev deploy：' + message, file=sys.stderr)
        sys.exit(1)
