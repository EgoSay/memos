#!/usr/bin/env python3
"""Restricted SSH gate: a stopped, verified pre-upgrade snapshot and timed rollback.

Only prepare, confirm and rollback are accepted; this key cannot run a shell or
read journal contents. Restore refuses to replace new content. Dokploy remains
responsible for deployment; its release branch pins the exact verified digest.
"""
import fcntl, hashlib, json, os, pathlib, re, shutil, sqlite3, subprocess, sys, time, signal
from contextlib import closing
from journal_snapshot import snapshot, verify
from journal_cloud_backup import run as backup
ROOT = pathlib.Path('/var/lib/memos-release')
CONFIG = pathlib.Path('/etc/memos-backup/config.json')
CONTAINER = 'memos-journal-ci55cj-memos-1'

def command(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True, timeout=60).stdout

def fingerprint(database):
    digest = hashlib.sha256()
    # Include account/settings changes too; a rollback must not erase any new state.
    # Stream the logical database instead of loading years of journal rows at once.
    with closing(sqlite3.connect(database.as_uri() + '?mode=ro', uri=True)) as db:
        for statement in db.iterdump():
            digest.update(statement.encode())
            digest.update(b'\n')
    return digest.hexdigest()

def validate(release):
    if set(release) != {'commit', 'digest'} or not re.fullmatch('[a-f0-9]{40}', release['commit']) or (not re.fullmatch('sha256:[a-f0-9]{64}', release['digest'])):
        raise ValueError('invalid release')
    return release

def wait_for_backup():
    deadline = time.monotonic() + 180
    while command('systemctl', 'show', '--property=ActiveState', '--value', 'memos-backup.service').strip() not in ('inactive', 'failed'):
        if time.monotonic() > deadline:
            raise TimeoutError('backup still running')
        time.sleep(2)

def prepare(release):
    validate(release)
    config = json.loads(CONFIG.read_text())
    source = pathlib.Path(config['source'])
    previous = json.loads(command('docker', 'inspect', CONTAINER))[0]
    image = json.loads(command('docker', 'image', 'inspect', previous['Image']))[0]['RepoDigests'][0]
    if not image.startswith('ghcr.io/egosay/memos-journal@sha256:'):
        raise ValueError('unexpected previous image')
    if (ROOT / 'pending.json').exists():
        raise RuntimeError('another release awaits confirmation')
    stage = ROOT / ('before-' + release['commit'] + '-' + str(int(time.time())))
    pending = {**release, 'status': 'preparing', 'previousImage': image, 'source': str(source), 'stage': str(stage), 'preparedTs': int(time.time())}
    (ROOT / 'pending.json').write_text(json.dumps(pending))
    command('systemd-run', '--collect', '--unit=memos-release-rollback', '--on-active=8m', '/usr/bin/python3', '/opt/memos-backup/journal_release_gate.py', 'rollback')

    def interrupted(*_):
        raise TimeoutError('preparation deadline exceeded')
    signal.signal(signal.SIGALRM, interrupted)
    signal.alarm(360)
    command('systemctl', 'stop', 'memos-backup.timer')
    try:
        wait_for_backup()
        command('docker', 'stop', '-t', '30', CONTAINER)
        report = snapshot(source, stage, pathlib.Path(config['logicalRoot']))
        receipt = backup(config)
        if receipt.get('status') != 'success':
            raise RuntimeError('pre-upgrade backup not completed: ' + receipt.get('status', 'unknown'))
        pending = {**release, 'status': 'prepared', 'previousImage': image, 'source': str(source), 'stage': str(stage), 'fingerprint': fingerprint(stage / 'data/memos_prod.db'), 'snapshotId': receipt['snapshotId'], 'preparedTs': int(time.time())}
        network = next((n for n in previous['NetworkSettings']['Networks'] if n.startswith('memos-journal-ci55cj_')))
        compose = f'services:\n  memos:\n    image: {image}\n    restart: unless-stopped\n    init: true\n    mem_limit: 384m\n    cpus: 0.5\n    environment:\n      TZ: Asia/Shanghai\n      MEMOS_PORT: "5230"\n      MEMOS_DATA: /var/opt/memos\n      MEMOS_DRIVER: sqlite\n      MEMOS_INSTANCE_URL: https://memos.cjwdream.top\n    volumes:\n      - memos-data:/var/opt/memos\n    healthcheck:\n      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:5230/healthz"]\n      interval: 10s\n      timeout: 5s\n      retries: 6\nvolumes:\n  memos-data:\n    external: true\n    name: memos-journal-ci55cj_memos-data\nnetworks:\n  default:\n    external: true\n    name: {network}\n'
        (ROOT / 'rollback.yaml').write_text(compose)
        (ROOT / 'pending.json').write_text(json.dumps(pending))
        return {'status': 'prepared', 'commit': release['commit'], 'snapshotId': receipt['snapshotId']}
    except Exception:
        command('docker', 'start', CONTAINER)
        command('systemctl', 'stop', 'memos-release-rollback.timer')
        (ROOT / 'pending.json').unlink(missing_ok=True)
        command('systemctl', 'start', 'memos-backup.timer')
        raise
    finally:
        signal.alarm(0)

def wait_for_release(pending):
    # HTTP can be ready before Docker's first scheduled health check completes.
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        try:
            d = json.loads(command('docker', 'inspect', CONTAINER))[0]
            digests = json.loads(command('docker', 'image', 'inspect', d['Image']))[0]['RepoDigests']
            if 'ghcr.io/egosay/memos-journal@' + pending['digest'] in digests and d['State'].get('Health', {}).get('Status') == 'healthy':
                return d
        except subprocess.CalledProcessError:
            pass  # Compose may briefly remove/recreate the named container.
        time.sleep(2)
    raise RuntimeError('wrong or unhealthy release after readiness deadline')

def confirm():
    pending = json.loads((ROOT / 'pending.json').read_text())
    d = wait_for_release(pending)
    address = next(iter(d['NetworkSettings']['Networks'].values()))['IPAddress']
    profile = json.loads(command('curl', '--fail', '--silent', 'http://' + address + ':5230/api/v1/instance/profile'))
    if profile.get('commit') != pending['commit'] or profile.get('needsSetup') or profile.get('accessMode') != 'INSTANCE_ACCESS_MODE_PRIVATE':
        raise RuntimeError('release identity or private mode mismatch')
    command('systemctl', 'stop', 'memos-release-rollback.timer')
    config = json.loads(CONFIG.read_text())
    config['instanceVersion'] = pending['commit']
    CONFIG.write_text(json.dumps(config))
    CONFIG.chmod(384)
    (ROOT / 'last-deployment.json').write_text(json.dumps({**pending, 'status': 'confirmed', 'confirmedTs': int(time.time())}))
    (ROOT / 'pending.json').unlink()
    command('systemctl', 'start', 'memos-backup.timer')
    for old in sorted(ROOT.glob('before-*'), key=lambda p: p.stat().st_mtime, reverse=True)[3:]:
        if old.is_dir():
            shutil.rmtree(old)
    return {'status': 'confirmed', 'commit': pending['commit'], 'preUpgradeSnapshot': pending['snapshotId']}

def rollback():
    file = ROOT / 'pending.json'
    if not file.exists():
        return {'status': 'no pending release'}
    pending = json.loads(file.read_text())
    if pending.get('status') == 'preparing':
        command('docker', 'start', CONTAINER)
        command('systemctl', 'stop', 'memos-release-rollback.timer')
        file.unlink()
        command('systemctl', 'start', 'memos-backup.timer')
        return {'status': 'preparation-aborted', 'previousImage': pending['previousImage']}
    source = pathlib.Path(pending['source'])
    stage = pathlib.Path(pending['stage'])
    verify(stage)
    command('docker', 'stop', '-t', '30', CONTAINER)
    # A missing or incompatible table must preserve data and restore availability too.
    try:
        unchanged = fingerprint(source / 'memos_prod.db') == pending['fingerprint']
    except Exception:
        command('docker', 'start', CONTAINER)
        command('systemctl', 'start', 'memos-backup.timer')
        raise RuntimeError('rollback blocked: unreadable data shape preserved')
    # Preserve writes or transformations accepted by the new version.
    if not unchanged:
        command('docker', 'start', CONTAINER)
        command('systemctl', 'start', 'memos-backup.timer')
        raise RuntimeError('rollback blocked: new content or changed data shape preserved')
    for p in source.iterdir():
        if p.name.startswith('memos_prod.db'):
            p.unlink()
    shutil.copy2(stage / 'data/memos_prod.db', source / 'memos_prod.db')
    os.chown(source / 'memos_prod.db', 10001, 10001)
    command('docker', 'compose', '-p', 'memos-journal-ci55cj', '-f', str(ROOT / 'rollback.yaml'), 'up', '-d', '--no-deps', 'memos')
    (ROOT / 'last-rollback.json').write_text(json.dumps({**pending, 'status': 'rolled-back', 'completedTs': int(time.time())}))
    file.unlink()
    command('systemctl', 'stop', 'memos-release-rollback.timer')
    command('systemctl', 'start', 'memos-backup.timer')
    return {'status': 'rolled-back', 'image': pending['previousImage']}

def main():
    os.umask(63)
    ROOT.mkdir(mode=448, parents=True, exist_ok=True)
    action = os.environ.get('SSH_ORIGINAL_COMMAND') or (sys.argv[1] if len(sys.argv) > 1 else '')
    if action not in ('prepare', 'confirm', 'rollback'):
        raise ValueError('command not allowed')
    with (ROOT / 'lock').open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        result = prepare(json.loads(sys.stdin.read(1025))) if action == 'prepare' else confirm() if action == 'confirm' else rollback()
        print(json.dumps(result))
if __name__ == '__main__':
    try:
        main()
    except Exception as e:
        if ROOT.exists():
            (ROOT / 'last-error.json').write_text(json.dumps({'action': os.environ.get('SSH_ORIGINAL_COMMAND', ''), 'errorType': type(e).__name__, 'message': str(e), 'attemptTs': int(time.time())}))
        print(type(e).__name__ + ': release gate failed; inspect private deployment receipts', file=sys.stderr)
        sys.exit(1)
