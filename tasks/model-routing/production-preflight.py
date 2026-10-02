"""Read-only production inventory; stream over SSH, never print credentials."""
import datetime
import hashlib
import json
import pathlib
import re
import subprocess
import urllib.request


def run(args, data=None):
    p = subprocess.run(args, input=data, capture_output=True, timeout=25)
    if p.returncode:
        raise RuntimeError('Read-only check failed: ' + args[0])
    return p.stdout.decode()


names = ['new-api-probe-49ee6e6-master', 'new-api-probe-49ee6e6-slave',
         'new-api-email-a', 'new-api-email-b']
containers = json.loads(run(['docker', 'inspect', *names]))
report = {'checked_at_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
          'containers': [], 'blockers': []}
for c in containers:
    env = dict(v.split('=', 1) for v in c['Config']['Env'] if '=' in v)
    image = json.loads(run(['docker', 'image', 'inspect', c['Image']]))[0]
    safe = {k: env.get(k) for k in ['NODE_TYPE', 'MEMORY_CACHE_ENABLED',
            'MODEL_ROUTING_MODE', 'MODEL_ROUTING_GROUPS', 'MODEL_ROUTING_MODELS',
            'MODEL_ROUTING_PRIORITY_GROUPS']}
    safe['redis_configured'] = bool(env.get('REDIS_CONN_STRING'))
    ips = [n['IPAddress'] for n in c['NetworkSettings']['Networks'].values() if n['IPAddress']]
    status = None
    if ips:
        try:
            with urllib.request.urlopen('http://' + ips[0] + ':3000/api/status', timeout=5) as response:
                status = {'http': response.status, 'success': json.load(response).get('success')}
        except (OSError, ValueError):
            status = {'http': None, 'success': False}
    report['containers'].append({'name': c['Name'], 'id': c['Id'], 'image_id': c['Image'],
        'revision': (image['Config'].get('Labels') or {}).get('org.opencontainers.image.revision'),
        'digests': image.get('RepoDigests', []), 'started': c['State']['StartedAt'],
        'restart_count': c['RestartCount'], 'running': c['State']['Running'],
        'networks': list(c['NetworkSettings']['Networks']), 'settings': safe, 'health': status})
    if 'probe-' in c['Name'] and not safe['redis_configured']:
        report['blockers'].append(c['Name'] + ': shared Redis connection is not configured')

caddy = pathlib.Path('/srv/new-api/cn2-20260921/Caddyfile').read_text()
report['caddy_sha256'] = hashlib.sha256(caddy.encode()).hexdigest()
report['route_targets'] = sorted(set(re.findall(r'new-api-[a-zA-Z0-9_-]+:3000', caddy)))
report['auth_routes_present'] = all(p in caddy for p in ['/api/verification', '/api/user/register', '/api/reset_password', '/api/user/reset'])

def sql(query):
    # Password remains expanded inside the existing DB container, not printed.
    return run(['docker', 'exec', '-i', 'new-api-cn2-mysql', 'sh', '-c',
        'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql -uroot --default-character-set=utf8mb4 -N -B -r'], query.encode())

db = 'new_api_production_20260921'
report['channels'] = [json.loads(line) for line in sql(
    "SELECT JSON_OBJECT('id',id,'status',status,'group',`group`,'models',models,'priority',priority,'weight',weight,'test_model',test_model) FROM "
    + db + '.channels ORDER BY id;').splitlines()]
keys = ['monitor_setting.auto_test_channel_enabled', 'monitor_setting.auto_test_channel_minutes',
        'monitor_setting.channel_test_concurrency', 'probe_setting.scheduling_protection_enabled',
        'probe_setting.scheduling_failure_threshold', 'probe_setting.scheduling_success_threshold']
report['probe_options'] = json.loads(sql("SELECT COALESCE(JSON_OBJECTAGG(`key`,value),JSON_OBJECT()) FROM "
    + db + '.options WHERE `key` IN (' + ','.join("'" + k + "'" for k in keys) + ');'))
print(json.dumps(report, ensure_ascii=False, indent=2))
