"""Authenticated acceptance on a restored database; never writes live data."""
import hashlib
import json
import pathlib
import runpy
import secrets
import sys
import time
import urllib.error
import urllib.request

deploy = runpy.run_path('/tmp/deploy-channel-cost.py')
run, sql, inspect = [deploy[key] for key in ('run', 'sql', 'inspect')]
root, image, revision = [deploy[key] for key in ('root', 'image', 'revision')]
clone = json.loads((root / 'backup.json').read_text())['restored_database']
assert clone.startswith('modelroute_verify_') and clone != deploy['db']
name = 'revenue-smoke-' + revision[:8]
account = 'rv_' + revision[:12]
password = secrets.token_hex(24)
envfile = root / 'smoke-private.env'
network_created = account_created = connected = False
container_id = None
checks = []

try:
    assert not sql("SELECT User FROM mysql.user WHERE User='" + account + "';")
    sql("CREATE USER '" + account + "'@'%' IDENTIFIED BY '" + password + "';")
    account_created = True
    sql("GRANT ALL ON `" + clone + "`.* TO '" + account + "'@'%';")
    run(['docker', 'network', 'create', '--internal', name])
    network_created = True
    run(['docker', 'network', 'connect', name, 'new-api-cn2-mysql'])
    connected = True
    sql('USE `' + clone + '`; UPDATE channels SET status=2;')
    envfile.write_text('SQL_DSN=' + account + ':' + password + '@tcp(new-api-cn2-mysql:3306)/' + clone + '?charset=utf8mb4&parseTime=True&loc=Local\nNODE_TYPE=slave\nMEMORY_CACHE_ENABLED=false\nMODEL_ROUTING_MODE=off\nSESSION_SECRET=' + secrets.token_hex(32) + '\n')
    container_id = run(['docker', 'run', '-d', '--name', name, '--network', name, '--env-file', str(envfile),
        '--memory', '512m', '--cpus', '0.5', '--read-only', '--cap-drop', 'ALL', '--security-opt', 'no-new-privileges:true',
        '--tmpfs', '/data:rw,size=64m', '--tmpfs', '/tmp:rw,size=64m', image]).decode().strip()
    deploy['healthy'](name, name)
    base = 'http://' + inspect([name])[0]['NetworkSettings']['Networks'][name]['IPAddress'] + ':3000'
    ids = sql('SELECT id FROM `' + clone + '`.users ORDER BY id LIMIT 3;').splitlines()
    assert len(ids) == 3 and all(value.isdigit() for value in ids)
    credentials = {}
    for user_id, role in zip(ids, (1, 10, 100)):
        sql('UPDATE `' + clone + '`.users SET role=' + str(role) + ',status=1 WHERE id=' + user_id + ';')
        token = 'nap_' + secrets.token_hex(24)
        scopes = json.dumps(['billing:read', 'option:read', 'option:write'])
        sql('INSERT INTO `' + clone + "`.user_access_tokens (user_id,name,token_hash,token_hint,scopes,expires_at,last_used_at,last_used_ip,created_at) VALUES (" + user_id + ",'release-smoke','" + hashlib.sha256(token.encode()).hexdigest() + "','test','" + scopes + "',0,0,''," + str(int(time.time())) + ');')
        credentials[role] = token

    def api(path, role=None, method='GET', body=None, expected=200):
        headers = {'Content-Type': 'application/json'}
        if role is not None:
            headers['Authorization'] = 'Bearer ' + credentials[role]
        request = urllib.request.Request(base + path, headers=headers, method=method,
            data=None if body is None else json.dumps(body).encode())
        try:
            with urllib.request.urlopen(request, timeout=20) as response:
                status, data = response.status, json.loads(response.read())
        except urllib.error.HTTPError as error:
            status, data = error.code, json.loads(error.read())
        assert status == expected, (path, role, status, data.get('code'))
        if expected == 200:
            assert data.get('success') is True, (path, data.get('message'))
        checks.append({'path': path, 'role': role, 'method': method, 'status': status})
        return data.get('data')

    end = int(time.time())
    query = '/api/revenue?start_timestamp=' + str(end - 86400) + '&end_timestamp=' + str(end)
    api(query, expected=401)
    api(query, 1, expected=403)
    report = api(query, 10)
    assert isinstance(report, dict) and 'net_sales' in report
    api('/api/revenue/prices', 10, expected=403)
    api('/api/revenue/prices', 10, 'PUT', {'rules': []}, expected=403)
    api('/api/revenue/prices/rules', 10, 'POST', {'channel_id': 32, 'model': 'release-smoke', 'unit': 'model_multiplier', 'unit_price': 0.75, 'source': 'isolated acceptance fixture'}, expected=403)
    api('/api/revenue/prices', 100)
    baseline = api('/api/revenue/prices', 100)
    appended = {'channel_id': 32, 'model': 'release-smoke', 'unit': 'model_multiplier', 'unit_price': 0.75, 'source': 'isolated acceptance fixture'}
    api('/api/revenue/prices/rules', 100, 'POST', appended)
    rules = api('/api/revenue/prices', 100)
    assert rules[:-1] == baseline and rules[-1]['channel_id'] == appended['channel_id']
    assert rules[-1]['model'] == appended['model'] and rules[-1]['unit'] == appended['unit']
    assert rules[-1]['unit_price'] == appended['unit_price'] and rules[-1]['effective_at'] > 0
    api('/api/revenue/prices', 100, 'PUT', {'rules': baseline}, expected=409)
    assert api('/api/revenue/prices', 100) == rules
    api('/api/option/', 100, 'PUT', {'key': 'RevenuePurchasePrices', 'value': '[]'}, expected=409)
    assert api('/api/revenue/prices', 100) == rules
    invalid = {**appended, 'model': 'release-invalid', 'unit_price': -1}
    api('/api/revenue/prices/rules', 100, 'POST', invalid, expected=400)
    assert api('/api/revenue/prices', 100) == rules
    assert deploy['request'](base, '/v1/videos/nonexistent')[0] == 401
    (root / 'authenticated-smoke.json').write_text(json.dumps({'checks': checks, 'database': clone,
        'production_data_written': False, 'paid_upstream_requests': 0}, indent=2))
    print('AUTHENTICATED_SMOKE_PASSED; admin report/root config/negative price/anonymous video; restored DB only')
finally:
    if container_id:
        deploy['stop_owned_container'](name, container_id, 15, True)
    if connected:
        run(['docker', 'network', 'disconnect', name, 'new-api-cn2-mysql'])
    if account_created:
        sql("DROP USER '" + account + "'@'%';")
    envfile.unlink(missing_ok=True)
