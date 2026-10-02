"""Provision only the authorized internal routing store; never alter live apps."""
import json
import os
import pathlib
import secrets
import subprocess
import time

os.umask(0o077)
root = pathlib.Path('/srv/new-api/model-routing-redis')
name = 'new-api-model-routing-redis'
network = 'new-api-model-routing'


def run(args, data=None):
    p = subprocess.run(args, input=data, capture_output=True, timeout=60)
    if p.returncode:
        raise RuntimeError('Provision command failed: ' + args[0] + ' ' + args[1])
    return p.stdout.decode().strip()


if root.exists():
    raise SystemExit('Existing routing Redis directory; inspect before retry, never overwrite credentials.')
if subprocess.run(['docker', 'inspect', name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
    raise SystemExit('Existing routing Redis container; inspect before retry.')
if subprocess.run(['docker', 'network', 'inspect', network], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
    raise SystemExit('Existing routing network; inspect before retry.')
image = json.loads(run(['docker', 'image', 'inspect', 'redis:7.4-alpine']))[0]
digest = next(d for d in image['RepoDigests'] if d.startswith('redis@sha256:'))
uid = int(run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'id', digest, '-u', 'redis']))
gid = int(run(['docker', 'run', '--rm', '--network', 'none', '--entrypoint', 'id', digest, '-g', 'redis']))
root.mkdir(mode=0o700)
data_dir = root / 'data'
data_dir.mkdir(mode=0o700)
os.chown(data_dir, uid, gid)
password = secrets.token_hex(32)
config = root / 'redis.conf'
config.write_text('bind 0.0.0.0\nprotected-mode yes\nport 6379\nrequirepass ' + password +
    '\ndir /data\nappendonly yes\nappendfsync everysec\nsave ""\nmaxmemory 96mb\nmaxmemory-policy noeviction\n')
os.chown(config, uid, gid)
os.chmod(config, 0o600)
(root / 'connection.env').write_text('MODEL_ROUTING_REDIS_URL=redis://:' + password + '@' + name + ':6379/0\n')
run(['docker', 'network', 'create', '--internal', network])
run(['docker', 'run', '-d', '--name', name, '--restart', 'unless-stopped', '--network', network,
     '--memory', '256m', '--cpus', '0.5', '--user', str(uid) + ':' + str(gid),
     '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true', '--read-only',
     '--log-opt', 'max-size=10m', '--log-opt', 'max-file=2',
     '--mount', 'type=bind,src=' + str(config) + ',dst=/usr/local/etc/redis/redis.conf,readonly',
     '--mount', 'type=bind,src=' + str(data_dir) + ',dst=/data',
     digest, 'redis-server', '/usr/local/etc/redis/redis.conf'])
# Password is supplied over stdin; neither argv nor report contains it.
ready = False
for _ in range(10):
    try:
        result = run(['docker', 'exec', '-i', name, 'sh', '-c',
            'read -r REDISCLI_AUTH; export REDISCLI_AUTH; exec redis-cli ping'], (password + '\n').encode())
        ready = result == 'PONG'
        if ready:
            break
    except RuntimeError:
        pass
    time.sleep(0.5)
if not ready:
    raise RuntimeError('Redis authentication/health check failed; live apps unchanged.')
obj = json.loads(run(['docker', 'inspect', name]))[0]
assert not obj['HostConfig']['PortBindings']
report = {'name': name, 'image': digest, 'network': network, 'public_ports': False,
          'authenticated_ping': True, 'live_apps_modified': False}
(root / 'report.json').write_text(json.dumps(report, indent=2))
print(json.dumps(report))
