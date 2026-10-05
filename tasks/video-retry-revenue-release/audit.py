"""Read-only post-release checks with bounded, non-secret evidence."""
import datetime
import json
import runpy

deploy = runpy.run_path('/tmp/deploy-revenue-video.py')
deploy['public_checks'](deploy['revision'])
sql, inspect = deploy['sql'], deploy['inspect']
db = deploy['db']
rules = sql("SELECT value FROM `" + db + "`.options WHERE `key`='RevenuePurchasePrices';")
rules_count = len(json.loads(rules)) if rules else 0
containers = inspect(deploy['new_names'] + deploy['old_names'])
record = {
    'time': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'public_checks': 'passed',
    'purchase_rules_count': rules_count,
    'containers': [{'name': value['Name'].lstrip('/'), 'id': value['Id'], 'image': value['Image'],
                    'running': value['State']['Running'], 'restart_count': value['RestartCount'],
                    'started_at': value['State']['StartedAt']} for value in containers],
    'production_data_written': False,
}
deploy['save']('post-release.json', record)
print(json.dumps(record, indent=2))
