"""Controlled shadow release. Keeps old applications, images and the live ledger.

Run on the authorized production host with action, immutable image and revision.
No secret is printed; private snapshots and backups are root-readable only.
"""
import datetime
import gzip
import hashlib
import json
import os
import pathlib
import re
import secrets
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from html.parser import HTMLParser

os.umask(0o077)
if sys.flags.optimize:
    raise RuntimeError('Release guardrails require normal Python mode; do not use -O or PYTHONOPTIMIZE')
action, image, revision = sys.argv[1:4]
assert re.fullmatch(r'[0-9a-f]{40}', revision)
assert re.fullmatch(r'ghcr.io/taow41866-collab/new-api@sha256:[0-9a-f]{64}', image)
root = pathlib.Path('/srv/new-api/releases/revenue-video-' + revision[:12])
old_names = ['new-api-revenue-video-90707e3f-master', 'new-api-revenue-video-90707e3f-slave']
new_names = ['new-api-revenue-video-' + revision[:8] + '-' + role for role in ['master', 'slave']]
frontend_revision = 'newapi-revenue-video-' + revision[:12]
canvas_sha = '95406ba36c6aa3d64c42605a63c53b521be238e458a84ed633eae46a3993a0e4'
canvas_revision = 'canvas-video-' + canvas_sha[:12]
site = pathlib.Path('/srv/new-api/cn2-20260921/original-config/site')
auth_names = ['new-api-email-a', 'new-api-email-b']
db = 'new_api_production_20260921'
caddy = pathlib.Path('/srv/new-api/cn2-20260921/Caddyfile')
watchdog = pathlib.Path('/srv/new-api/cn2-20260921/scripts/production_watchdog.py')


def run(args, data=None, timeout=120):
    p = subprocess.run(args, input=data, capture_output=True, timeout=timeout)
    if p.returncode:
        if root.exists():
            (root / 'last-error-private.log').write_bytes(p.stderr)
        raise RuntimeError('Release command failed: ' + args[0] + '; inspect private log')
    return p.stdout


def inspect(names):
    return json.loads(run(['docker', 'inspect', *names]))


def sql(statement):
    return run(['docker', 'exec', '-i', 'new-api-cn2-mysql', 'sh', '-c',
        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --default-character-set=utf8mb4 -N -B -r'], statement.encode()).decode().strip()


def save(name, value):
    (root / name).write_text(json.dumps(value, indent=2))


def load(name):
    return json.loads((root / name).read_text())


def sha(data):
    return hashlib.sha256(data).hexdigest()


def request(base, path):
    try:
        with urllib.request.urlopen(base + path, timeout=5) as r:
            return r.status, r.read(), dict(r.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read(), dict(e.headers)


def healthy(name, network='new-api-cn2_database'):
    obj = inspect([name])[0]
    address = 'http://' + obj['NetworkSettings']['Networks'][network]['IPAddress'] + ':3000'
    for _ in range(30):
        try:
            code, body, _ = request(address, '/api/status')
            if code == 200 and json.loads(body).get('success'):
                break
        except (OSError, ValueError):
            pass
        time.sleep(1)
    else:
        raise RuntimeError('Candidate health check failed')
    checks = {}
    for path, expected in [('/api/status', 200), ('/docs/', 200), ('/api/user/self', 401), ('/v1/models', 401)]:
        checks[path] = request(address, path)[0]
        assert checks[path] == expected, (name, path, checks[path])
    return checks


def clone_application(source, name, connection, created_ids):
    # Match the proven prior release path: create on DB, then attach egress and
    # the internal routing network. Never import inspect-only Docker fields.
    config, host = source['Config'], source['HostConfig']
    assert set(source['NetworkSettings']['Networks']) == {'new-api-cn2_database', 'new-api-cn2_egress', 'new-api-model-routing'}
    assert not host.get('PortBindings') and not host.get('Privileged')
    assert config['Entrypoint'] == ['/new-api']
    env = dict(item.split('=', 1) for item in config['Env'])
    assert not env.get('REDIS_CONN_STRING') and env.get('MEMORY_CACHE_ENABLED') == 'false'
    assert env.get('MODEL_ROUTING_MODE') == 'shadow' and env.get('MODEL_ROUTING_REDIS_URL') == connection
    env['NODE_NAME'] = name
    assert all('\n' not in value and '\r' not in value for value in env.values())
    envfile = root / (name + '.env')
    args = ['docker', 'create', '--name', name, '--restart', 'unless-stopped',
            '--network', 'new-api-cn2_database', '--env-file', str(envfile)]
    if config.get('User'):
        args += ['--user', config['User']]
    if config.get('WorkingDir'):
        args += ['--workdir', config['WorkingDir']]
    if host.get('ReadonlyRootfs'):
        args.append('--read-only')
    for cap in host.get('CapDrop') or []:
        args += ['--cap-drop', cap]
    for option in host.get('SecurityOpt') or []:
        args += ['--security-opt', option]
    for target, options in (host.get('Tmpfs') or {}).items():
        args += ['--tmpfs', target + ':' + options]
    if host.get('Memory'):
        args += ['--memory', str(host['Memory'])]
    if host.get('NanoCpus'):
        args += ['--cpus', str(host['NanoCpus'] / 1_000_000_000)]
    if host.get('PidsLimit'):
        args += ['--pids-limit', str(host['PidsLimit'])]
    for mount in source['Mounts']:
        assert mount['Type'] == 'bind'
        args += ['--mount', 'type=bind,src=' + mount['Source'] + ',dst=' + mount['Destination'] +
                 ('' if mount['RW'] else ',readonly')]
    args += ['--entrypoint', '/new-api', image, *(config.get('Cmd') or [])]
    try:
        envfile.write_text(''.join(key + '=' + value + '\n' for key, value in env.items()))
        container_id = run(args).decode().strip()
        assert re.fullmatch(r'[0-9a-f]{64}', container_id), 'Docker create returned no usable container ID'
        created_ids[name] = container_id
    finally:
        envfile.unlink(missing_ok=True)
    run(['docker', 'network', 'connect', '--gw-priority', '1', 'new-api-cn2_egress', name])
    run(['docker', 'network', 'connect', 'new-api-model-routing', name])
    run(['docker', 'start', name])


def routing_redis_call(name, connection, *command):
    parsed = urllib.parse.urlparse(connection)
    assert parsed.scheme == 'redis' and parsed.hostname == 'new-api-model-routing-redis' and parsed.password
    assert parsed.username in (None, ''), 'Redis check must use the same default ACL user as the application'
    assert parsed.port == 6379 and parsed.path == '/0' and not parsed.query and not parsed.fragment
    redis_image = inspect(['new-api-model-routing-redis'])[0]['Image']
    args = ['docker', 'run', '--rm', '-i', '--network', 'container:' + name,
            '--entrypoint', 'sh', redis_image, '-c',
            'read -r REDISCLI_AUTH; export REDISCLI_AUTH; exec redis-cli -h new-api-model-routing-redis "$@"',
            'sh', *command]
    return run(args, (parsed.password + '\n').encode()).strip()


def verify_candidate_network(name, connection):
    obj = inspect([name])[0]
    networks = obj['NetworkSettings']['Networks']
    assert set(networks) == {'new-api-cn2_database', 'new-api-cn2_egress', 'new-api-model-routing'}
    assert networks['new-api-cn2_egress']['GwPriority'] == 1
    assert networks['new-api-cn2_egress']['Gateway']
    assert not obj['HostConfig'].get('PortBindings')
    env = dict(item.split('=', 1) for item in obj['Config']['Env'])
    assert env.get('MODEL_ROUTING_MODE') == 'shadow' and env.get('MODEL_ROUTING_REDIS_URL') == connection
    assert routing_redis_call(name, connection, 'PING') == b'PONG'
    # An internal health check cannot prove the real upstream egress path.
    redis_image = inspect(['new-api-model-routing-redis'])[0]['Image']
    run(['docker', 'run', '--rm', '--network', 'container:' + name,
         '--entrypoint', 'sh', redis_image, '-c',
         'wget -q -T 5 -O /dev/null https://lpss.online/api/status'], timeout=30)


def verify_shared_redis(connection):
    key = 'model-route:deploy-check:' + secrets.token_hex(12)
    value = secrets.token_hex(16)
    assert routing_redis_call(new_names[0], connection, 'SET', key, value, 'EX', '60') == b'OK'
    try:
        assert routing_redis_call(new_names[1], connection, 'GET', key) == value.encode(), 'Candidates do not share routing Redis'
    finally:
        routing_redis_call(new_names[0], connection, 'DEL', key)


def verify_old_identity(stage_record, require_running):
    old_now = inspect(old_names)
    for name, obj, expected in zip(old_names, old_now, stage_record['old_nodes']):
        assert obj['Name'].lstrip('/') == name
        assert obj['Id'] == expected['id'] and obj['Image'] == expected['image'], 'Old container changed since stage'
        assert not require_running or obj['State']['Running'], 'Old serving container stopped before switch'
    return old_now


def verify_release_identity(stage_record, require_old_running):
    verify_old_identity(stage_record, require_old_running)
    new_now = inspect(new_names)
    for name, obj, expected in zip(new_names, new_now, stage_record['new_nodes']):
        assert obj['Name'].lstrip('/') == name
        assert obj['Id'] == expected['id'] and obj['Image'] == expected['image'], 'Candidate changed since stage'
        assert obj['Config']['Image'] == image, 'Candidate image changed'
        assert obj['State']['Running'], 'Candidate stopped before switch'
    return new_now


def verify_master_overlap(old_nodes):
    # This release stages a second master. Only jobs with database concurrency
    # protection may overlap until switch stops the old master.
    assert len(old_nodes) == 2
    for obj, expected_node_type in zip(old_nodes, ('master', 'slave')):
        env = dict(item.split('=', 1) for item in obj['Config']['Env'])
        assert env.get('NODE_TYPE') == expected_node_type, 'Unexpected node role'
        assert not env.get('CHANNEL_UPDATE_FREQUENCY'), 'Unprotected channel balance updater is enabled'
        assert env.get('BATCH_UPDATE_ENABLED', 'false') != 'true', 'Unprotected batch updater is enabled'
    assert int(sql('SELECT COUNT(*) FROM `' + db + '`.channels WHERE type=57;')) == 0, \
        'Codex credential refresh cannot overlap between masters'


def owned_candidates(stage_record):
    owned = {}
    unresolved = []
    for name, expected in zip(new_names, stage_record['new_nodes']):
        try:
            result = subprocess.run(['docker', 'inspect', expected['id']], capture_output=True, timeout=30)
        except Exception:
            unresolved.append(name)
            continue
        if result.returncode:
            if not re.search(rb'No such (object|container)', result.stderr, re.IGNORECASE):
                unresolved.append(name)
            continue
        try:
            obj = json.loads(result.stdout)[0]
        except (ValueError, IndexError, KeyError):
            unresolved.append(name)
            continue
        if obj['Id'] == expected['id'] and obj['Image'] == expected['image']:
            owned[name] = expected['id']
        else:
            unresolved.append(name)
    return owned, unresolved


def stop_owned_container(name, expected_id, timeout, disable_restart):
    if not expected_id:
        return False
    try:
        obj = inspect([expected_id])[0]
    except Exception:
        return False
    if obj['Id'] != expected_id:
        return False
    if disable_restart:
        run(['docker', 'update', '--restart', 'no', expected_id])
    if obj['State']['Running']:
        run(['docker', 'stop', '-t', str(timeout), expected_id], timeout=timeout + 30)
        try:
            stopped = inspect([expected_id])[0]
        except Exception:
            return False
        return stopped['Id'] == expected_id and not stopped['State']['Running']
    return True


def assert_owned_name(name, expected_id):
    assert inspect([name])[0]['Id'] == expected_id, 'Rehearsal container name no longer belongs to this release'


def schema(database):
    return sha(sql("SELECT TABLE_NAME,COLUMN_NAME,COLUMN_TYPE,IS_NULLABLE,COLUMN_DEFAULT FROM information_schema.COLUMNS WHERE TABLE_SCHEMA='" + database + "' ORDER BY TABLE_NAME,ORDINAL_POSITION;").encode())


def ledger(database):
    queries = ['SELECT id,quota,used_quota,request_count FROM users ORDER BY id',
               'SELECT * FROM user_subscriptions ORDER BY id', 'SELECT * FROM redemptions ORDER BY id',
               'SELECT * FROM subscription_plans ORDER BY id']
    return [sha(sql('USE `' + database + '`;' + q).encode()) for q in queries]


def rehearse(clone):
    # All temporary grants are removed even if network or container setup fails.
    rehearsal = 'mr-rehearsal-' + revision[:8]
    account = 'mr_' + revision[:12]
    password = secrets.token_hex(24)
    envfile = root / 'rehearsal.env'
    assert not sql("SELECT User FROM mysql.user WHERE User='" + account + "';")
    account_created = False
    mysql_connected = False
    container_id = None
    try:
        sql("CREATE USER '" + account + "'@'%' IDENTIFIED BY '" + password + "';")
        account_created = True
        sql("GRANT ALL ON `" + clone + "`.* TO '" + account + "'@'%';")
        sql('USE `' + clone + "`; UPDATE options SET value='false' WHERE `key`='monitor_setting.auto_test_channel_enabled';")
        run(['docker', 'network', 'create', '--internal', rehearsal])
        run(['docker', 'network', 'connect', rehearsal, 'new-api-cn2-mysql'])
        mysql_connected = True
        original_ledger, original_schema = ledger(clone), schema(clone)
        envfile.write_text('SQL_DSN=' + account + ':' + password + '@tcp(new-api-cn2-mysql:3306)/' + clone + '?charset=utf8mb4&parseTime=True&loc=Local\nNODE_TYPE=master\nMEMORY_CACHE_ENABLED=false\nMODEL_ROUTING_MODE=off\nSESSION_SECRET=' + secrets.token_hex(32) + '\n')
        container_id = run(['docker', 'run', '-d', '--name', rehearsal, '--network', rehearsal, '--env-file', str(envfile),
             '--memory', '512m', '--cpus', '0.5', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
             '--read-only', '--tmpfs', '/data:rw,size=64m', '--tmpfs', '/tmp:rw,size=64m',
             '--entrypoint', '/new-api', image, '--log-dir', '/data/logs']).decode().strip()
        assert re.fullmatch(r'[0-9a-f]{64}', container_id), 'Rehearsal container ID unavailable'
        assert_owned_name(rehearsal, container_id)
        checks = healthy(rehearsal, rehearsal)
        assert ledger(clone) == original_ledger
        assert schema(clone) == original_schema, 'Unexpected migration; do not deploy'
        run(['docker', 'restart', container_id])
        assert_owned_name(rehearsal, container_id)
        healthy(rehearsal, rehearsal)
        assert ledger(clone) == original_ledger and schema(clone) == original_schema
        save('rehearsal.json', {'checks': checks, 'ledger_unchanged': True, 'schema_unchanged_after_two_starts': True})
    finally:
        cleanup_errors = []
        if container_id:
            try:
                if not stop_owned_container(rehearsal, container_id, 15, False):
                    cleanup_errors.append('confirm or stop rehearsal')
            except Exception:
                cleanup_errors.append('stop rehearsal')
        if mysql_connected:
            try:
                run(['docker', 'network', 'disconnect', rehearsal, 'new-api-cn2-mysql'])
            except Exception:
                cleanup_errors.append('disconnect MySQL')
        if account_created:
            try:
                sql("DROP USER '" + account + "'@'%';")
            except Exception:
                cleanup_errors.append('drop temporary database account')
        envfile.unlink(missing_ok=True)
        if cleanup_errors:
            raise RuntimeError('Rehearsal cleanup incomplete: ' + ', '.join(cleanup_errors))


def stage():
    assert not root.exists(), 'Release directory exists: inspect, never overwrite or blindly retry'
    assert all(subprocess.run(['docker', 'inspect', n], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 0 for n in new_names)
    objects = inspect(old_names)
    assert all(o['State']['Running'] for o in objects)
    verify_master_overlap(objects)
    current_image = json.loads(run(['docker', 'image', 'inspect', image]))[0]
    assert current_image['Architecture'] == 'amd64'
    assert current_image['Config']['Labels']['org.opencontainers.image.revision'] == revision
    root.mkdir(mode=0o700)
    save('before-private.json', objects)
    save('auth-before-private.json', inspect(auth_names))
    before = caddy.read_bytes()
    (root / 'Caddyfile.before').write_bytes(before)
    (root / 'watchdog.before').write_bytes(watchdog.read_bytes())
    after = before.decode()
    for old, new in zip(old_names, new_names):
        assert old + ':3000' in after
        after = after.replace(old + ':3000', new + ':3000')
    after, count = re.subn(r'X-Newapi-Release\s+"[0-9a-f]+"', 'X-Newapi-Release "' + revision + '"', after)
    assert count == 1
    start = after.index('    handle @frontend {')
    end = after.index('\n    handle {\n        reverse_proxy', start)
    # New HTML/assets come from the CI-built image. Old hashed assets stay
    # available for clients holding an earlier HTML document during rollout.
    frontend = '''    handle @frontend {
        header Cache-Control "no-cache"
        header X-Newapi-Frontend-Revision "''' + frontend_revision + '''"
        root * /srv/newapi-canvas-nav-f29ae12048c8
        @retained_frontend_asset {
            path /static/*
            file {path}
        }
        handle @retained_frontend_asset {
            file_server
        }
        handle {
            reverse_proxy ''' + new_names[0] + ''':3000
        }
    }
'''
    after = after[:start] + frontend + after[end:]
    (root / 'Caddyfile.after').write_text(after)

    raw = run(['docker', 'exec', 'new-api-cn2-mysql', 'sh', '-c',
        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysqldump -uroot --single-transaction --quick --routines --triggers --hex-blob --set-gtid-purged=OFF ' + db])
    assert not re.search(rb'(?im)^\s*(USE\s|CREATE DATABASE)', raw)
    backup = root / 'database-preswitch.sql.gz'
    with gzip.open(backup, 'wb') as f:
        f.write(raw)
    assert gzip.decompress(backup.read_bytes()) == raw
    clone = 'modelroute_verify_' + revision[:12]
    assert not sql("SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME='" + clone + "';")
    sql('CREATE DATABASE `' + clone + '` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;')
    sql('USE `' + clone + '`;\n' + raw.decode())
    tables = int(sql("SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='" + clone + "';"))
    assert tables == len(re.findall(rb'(?m)^CREATE TABLE ', raw))
    save('backup.json', {'file': str(backup), 'sha256': sha(backup.read_bytes()), 'restored_database': clone, 'tables': tables})
    # Rehearsal has only the restored database, limited account, and no internet.
    rehearse(clone)
    connection = pathlib.Path('/srv/new-api/model-routing-redis/connection.env').read_text().strip().split('=', 1)[1]
    created_ids = {}
    try:
        for source, name in zip(objects, new_names):
            clone_application(source, name, connection, created_ids)
            healthy(name)
            verify_candidate_network(name, connection)
        verify_shared_redis(connection)
        assert caddy.read_bytes() == before, 'Caddy changed during stage'
        candidates = inspect(new_names)
        assert [obj['Id'] for obj in candidates] == [created_ids[name] for name in new_names]
        save('stage.json', {'revision': revision, 'image': image, 'mode': 'shadow',
            'old_nodes': [{'id': obj['Id'], 'image': obj['Image']} for obj in objects],
            'new_nodes': [{'id': obj['Id'], 'image': obj['Image']} for obj in candidates],
            'traffic_switched': False})
    except Exception as error:
        # A preflight name check is not ownership: another operator could have
        # created the name during rehearsal. Stop only IDs created by this run.
        unresolved = []
        for name, container_id in created_ids.items():
            try:
                if not stop_owned_container(name, container_id, 60, True):
                    unresolved.append(name)
            except Exception:
                unresolved.append(name)
        if unresolved:
            raise RuntimeError('Stage failed; candidate cleanup incomplete: ' + ', '.join(unresolved)) from error
        raise
    print('STAGED_AND_REHEARSED; traffic unchanged; backup restored and verified')


def reload_routes(content):
    run(['docker', 'exec', '-i', 'new-api-cn2-caddy', 'caddy', 'validate', '--config', '/dev/stdin', '--adapter', 'caddyfile'], content)
    caddy.write_bytes(content)  # Keep the inode used by the existing bind mount.
    run(['docker', 'kill', '--signal=SIGUSR1', 'new-api-cn2-caddy'])


class Assets(HTMLParser):
    def __init__(self):
        super().__init__()
        self.paths = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == 'script' and attrs.get('src'):
            self.paths.append(attrs['src'])
        if tag == 'link' and attrs.get('rel') in ('stylesheet', 'icon'):
            self.paths.append(attrs['href'])


def static_stage():
    verify_release_identity(load('stage.json'), require_old_running=True)
    assert caddy.read_bytes() == (root / 'Caddyfile.before').read_bytes()
    # Canvas is independently deployed; validate and reuse its live assets.
    target = site / canvas_revision
    assert target.is_dir() and (target / 'index.html').is_file()
    parser = Assets()
    parser.feed((target / 'index.html').read_text())
    assert parser.paths and all(p.startswith('/canvas/') for p in parser.paths)
    assert all((target / p.removeprefix('/canvas/')).is_file() for p in parser.paths)
    after = (root / 'Caddyfile.after').read_text()
    assert '/srv/' + canvas_revision in after
    assert 'X-Canvas-Revision "' + canvas_revision + '"' in after
    run(['docker', 'exec', '-i', 'new-api-cn2-caddy', 'caddy', 'validate', '--config', '/dev/stdin', '--adapter', 'caddyfile'], after.encode())
    run(['docker', 'exec', 'new-api-cn2-caddy', 'test', '-r', '/srv/' + canvas_revision + '/index.html'])
    (root / 'Caddyfile.after').write_text(after)
    save('static-stage.json', {'canvas_revision': canvas_revision, 'canvas_source': '750fdc8498d333c4eb4f356add263680bf2a9207',
        'index_sha256': sha((target / 'index.html').read_bytes()),
        'assets': parser.paths, 'old_assets_retained': True, 'reused_existing_assets': True,
        'newapi_frontend': 'embedded in immutable image'})
    print('STATIC_VALIDATED_AND_REUSED; live routes unchanged')


def public_checks(expected):
    for _ in range(10):
        code, _, headers = request('https://lpss.online', '/api/status')
        if code == 200 and headers.get('X-Newapi-Release') == expected:
            break
        time.sleep(1)
    else:
        raise RuntimeError('Public revision did not converge')
    for path in ['/docs/', '/healthz']:
        assert request('https://lpss.online', path)[0] == 200
    assert request('https://lpss.online', '/api/user/self')[0] == 401
    assert request('https://lpss.online', '/api/revenue?start_timestamp=0&end_timestamp=1')[0] == 401
    assert request('https://lpss.online', '/api/revenue/prices')[0] == 401
    if expected == revision:
        code, body, headers = request('https://lpss.online', '/dashboard')
        assert code == 200 and headers.get('X-Newapi-Frontend-Revision') == frontend_revision
        candidate = inspect([new_names[0]])[0]
        address = 'http://' + candidate['NetworkSettings']['Networks']['new-api-cn2_database']['IPAddress'] + ':3000'
        assert body == request(address, '/')[1], 'Served frontend is not the CI-built image'
        parser = Assets()
        parser.feed(body.decode())
        for path in parser.paths:
            code, content, headers = request('https://lpss.online', path)
            assert code == 200
            assert content == request(address, path)[1], 'Frontend asset mismatch: ' + path
            if path.endswith('.js'):
                assert 'javascript' in headers.get('Content-Type', '')
            if path.endswith('.css'):
                assert 'text/css' in headers.get('Content-Type', '')
        static = load('static-stage.json')
        target = site / canvas_revision
        for path in ['/canvas/', '/canvas/video', '/canvas/canvas']:
            code, content, headers = request('https://lpss.online', path)
            assert code == 200 and content == (target / 'index.html').read_bytes()
            assert headers.get('X-Canvas-Revision') == canvas_revision
        for path in static['assets']:
            code, content, headers = request('https://lpss.online', path)
            assert code == 200 and content == (target / path.removeprefix('/canvas/')).read_bytes()
            if path.endswith('.js'):
                assert 'javascript' in headers.get('Content-Type', '')
            if path.endswith('.css'):
                assert 'text/css' in headers.get('Content-Type', '')
    # Compare auth container identity directly; never send real verification emails.
    old_auth = load('auth-before-private.json')
    now_auth = inspect(auth_names)
    assert [(o['Id'], o['State']['StartedAt']) for o in old_auth] == [(o['Id'], o['State']['StartedAt']) for o in now_auth]


def rollback():
    stage_record = load('stage.json')
    assert stage_record['revision'] == revision and stage_record['image'] == image
    verify_old_identity(stage_record, require_running=False)
    before, after = (root / 'Caddyfile.before').read_bytes(), (root / 'Caddyfile.after').read_bytes()
    assert caddy.read_bytes() in [before, after], 'Concurrent route edits; manual reconciliation required'
    for obj in load('before-private.json'):
        name = obj['Name'].lstrip('/')
        container_id = obj['Id']
        run(['docker', 'update', '--restart', obj['HostConfig']['RestartPolicy']['Name'], container_id])
        run(['docker', 'start', container_id])
        healthy(name)
    reload_routes(before)
    previous_revision = re.search(rb'X-Newapi-Release\s+"([0-9a-f]{40})"', before)
    assert previous_revision, 'Missing previous release marker; do not stop candidates'
    public_checks(previous_revision.group(1).decode())
    unresolved_configuration = []
    if (root / 'watchdog.after').exists():
        try:
            original_watchdog = (root / 'watchdog.before').read_bytes()
            current_watchdog = watchdog.read_bytes()
            if current_watchdog not in (original_watchdog, (root / 'watchdog.after').read_bytes()):
                unresolved_configuration.append('watchdog')
            elif current_watchdog != original_watchdog:
                watchdog.write_bytes(original_watchdog)
                if watchdog.read_bytes() != original_watchdog:
                    unresolved_configuration.append('watchdog')
        except Exception:
            unresolved_configuration.append('watchdog')
    owned, unresolved = owned_candidates(stage_record)
    stopped = []
    for name, container_id in owned.items():
        try:
            if stop_owned_container(name, container_id, 120, True):
                stopped.append(name)
            else:
                unresolved.append(name)
        except Exception:
            unresolved.append(name)
    public_checks(previous_revision.group(1).decode())
    save('rollback.json', {'status': 'partial' if unresolved or unresolved_configuration else 'complete',
        'ledger_restored': False, 'stopped_owned_candidates': stopped,
        'unresolved_candidates': unresolved,
        'unresolved_configuration': unresolved_configuration,
        'confirmed_absent_candidates': [name for name in new_names if name not in owned and name not in unresolved],
        'time': datetime.datetime.now(datetime.timezone.utc).isoformat()})
    if unresolved or unresolved_configuration:
        raise RuntimeError('Rollback incomplete; inspect candidates/configuration: ' +
                           ', '.join(unresolved + unresolved_configuration))
    print('ROLLED_BACK_APPLICATIONS_AND_ROUTES; ledger preserved')


def switch():
    stage_record = load('stage.json')
    assert (root / 'authenticated-smoke.json').is_file() and (root / 'static-stage.json').is_file()
    assert stage_record['revision'] == revision and stage_record['image'] == image
    assert not (root / 'switched.json').exists(), 'Already switched; do not repeat'
    assert caddy.read_bytes() == (root / 'Caddyfile.before').read_bytes()
    assert watchdog.read_bytes() == (root / 'watchdog.before').read_bytes()
    verify_release_identity(stage_record, require_old_running=True)
    connection = pathlib.Path('/srv/new-api/model-routing-redis/connection.env').read_text().strip().split('=', 1)[1]
    for name in new_names:
        healthy(name)
        verify_candidate_network(name, connection)
    verify_shared_redis(connection)
    verify_release_identity(stage_record, require_old_running=True)
    new_watchdog = watchdog.read_text()
    for old, new in zip(old_names, new_names):
        assert old in new_watchdog
        new_watchdog = new_watchdog.replace(old, new)
    (root / 'watchdog.after').write_text(new_watchdog)
    try:
        verify_master_overlap(inspect(old_names))
        reload_routes((root / 'Caddyfile.after').read_bytes())
        public_checks(revision)
        watchdog.write_text(new_watchdog)
        for expected in stage_record['old_nodes']:
            run(['docker', 'update', '--restart', 'no', expected['id']])
            run(['docker', 'stop', '-t', '120', expected['id']], timeout=150)
        public_checks(revision)
        save('switched.json', {'revision': revision, 'image': image, 'mode': 'shadow', 'old_containers_retained': old_names,
            'time': datetime.datetime.now(datetime.timezone.utc).isoformat()})
        print('SHADOW_DEPLOYED; legacy selection preserved; old containers and images retained')
    except Exception:
        rollback()
        raise


if __name__ == '__main__':
    assert action in ['stage', 'static-stage', 'switch', 'rollback', 'verify']
    {'stage': stage, 'static-stage': static_stage, 'switch': switch, 'rollback': rollback, 'verify': lambda: public_checks(revision)}[action]()
