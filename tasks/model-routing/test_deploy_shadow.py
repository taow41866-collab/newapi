"""Local contract checks for the release guardrails; no Docker or SSH calls."""
import importlib.util
import pathlib
import sys
import tempfile
import unittest
from unittest import mock


SCRIPT = pathlib.Path(__file__).with_name('deploy-shadow.py')
REVISION = 'a' * 40
IMAGE = 'ghcr.io/taow41866-collab/new-api@sha256:' + 'b' * 64


def load_script():
    spec = importlib.util.spec_from_file_location('deploy_shadow_tested', SCRIPT)
    module = importlib.util.module_from_spec(spec)
    with mock.patch.object(sys, 'argv', [str(SCRIPT), 'stage', IMAGE, REVISION]):
        spec.loader.exec_module(module)
    return module


class ReleaseGuardTests(unittest.TestCase):
    def setUp(self):
        self.release = load_script()
        self.stage = {
            'old_nodes': [{'id': 'old-master', 'image': 'old-image'},
                          {'id': 'old-slave', 'image': 'old-image'}],
            'new_nodes': [{'id': 'new-master', 'image': 'new-image'},
                          {'id': 'new-slave', 'image': 'new-image'}],
        }

    def nodes(self):
        old = [{'Name': '/' + name, 'Id': item['id'], 'Image': item['image'],
                'Config': {'Env': ['NODE_TYPE=' + ('master' if name.endswith('master') else 'slave'),
                                   'BATCH_UPDATE_ENABLED=false']},
                'State': {'Running': True}}
               for name, item in zip(self.release.old_names, self.stage['old_nodes'])]
        new = [{'Name': '/' + name, 'Id': item['id'], 'Image': item['image'],
                'Config': {'Image': IMAGE}, 'State': {'Running': True}}
               for name, item in zip(self.release.new_names, self.stage['new_nodes'])]
        return old, new

    def test_switch_rejects_replaced_old_or_candidate_container(self):
        old, new = self.nodes()
        with mock.patch.object(self.release, 'inspect', side_effect=[old, new]):
            self.release.verify_release_identity(self.stage, require_old_running=True)
        old[0]['Id'] = 'replaced'
        with mock.patch.object(self.release, 'inspect', side_effect=[old, new]):
            with self.assertRaisesRegex(AssertionError, 'Old container changed'):
                self.release.verify_release_identity(self.stage, require_old_running=True)
        old[0]['Id'] = 'old-master'
        new[1]['Config']['Image'] = 'unexpected-image'
        with mock.patch.object(self.release, 'inspect', side_effect=[old, new]):
            with self.assertRaisesRegex(AssertionError, 'Candidate image changed'):
                self.release.verify_release_identity(self.stage, require_old_running=True)

    def test_candidate_requires_egress_priority_and_routing_connection(self):
        candidate = {
            'NetworkSettings': {'Networks': {
                'new-api-cn2_database': {},
                'new-api-cn2_egress': {'GwPriority': 1, 'Gateway': '172.23.0.1'},
                'new-api-model-routing': {},
            }},
            'HostConfig': {'PortBindings': {}},
            'Config': {'Env': ['MODEL_ROUTING_MODE=shadow', 'MODEL_ROUTING_REDIS_URL=redis://:test@new-api-model-routing-redis:6379/0']},
        }
        redis = {'Image': 'sha256:redis-image'}
        with mock.patch.object(self.release, 'inspect', side_effect=[[candidate], [redis]]), \
                mock.patch.object(self.release, 'routing_redis_call', return_value=b'PONG') as ping, \
                mock.patch.object(self.release, 'run') as run:
            self.release.verify_candidate_network('candidate', 'redis://:test@new-api-model-routing-redis:6379/0')
            ping.assert_called_once()
            run.assert_called_once()
        candidate['NetworkSettings']['Networks']['new-api-cn2_egress']['GwPriority'] = 0
        with mock.patch.object(self.release, 'inspect', return_value=[candidate]):
            with self.assertRaises(AssertionError):
                self.release.verify_candidate_network('candidate', 'redis://:test@new-api-model-routing-redis:6379/0')

    def test_candidates_read_the_same_disposable_redis_value(self):
        values = {}

        def command(name, connection, *args):
            if args[0] == 'SET':
                values[args[1]] = args[2]
                return b'OK'
            if args[0] == 'GET':
                return values[args[1]].encode()
            return str(bool(values.pop(args[1], None))).encode()

        with mock.patch.object(self.release, 'routing_redis_call', side_effect=command) as call:
            self.release.verify_shared_redis('redis://:test@new-api-model-routing-redis:6379/0')
            self.assertEqual(3, call.call_count)
            self.assertFalse(values)

    def test_redis_check_cannot_use_a_different_port_than_the_app(self):
        with self.assertRaises(AssertionError):
            self.release.routing_redis_call('candidate', 'redis://:test@new-api-model-routing-redis:6380/0', 'PING')
        with self.assertRaises(AssertionError):
            self.release.routing_redis_call('candidate', 'redis://alice:test@new-api-model-routing-redis:6379/0', 'PING')

    def test_stage_rejects_unprotected_duplicate_master_jobs(self):
        old, _ = self.nodes()
        with mock.patch.object(self.release, 'sql', return_value='1'):
            with self.assertRaisesRegex(AssertionError, 'Codex'):
                self.release.verify_master_overlap(old)
        with mock.patch.object(self.release, 'sql', return_value='0'):
            self.release.verify_master_overlap(old)
            old[0]['Config']['Env'].append('CHANNEL_UPDATE_FREQUENCY=15')
            with self.assertRaisesRegex(AssertionError, 'channel balance'):
                self.release.verify_master_overlap(old)
            old[0]['Config']['Env'].pop()
            old[0]['Config']['Env'].append('BATCH_UPDATE_ENABLED=true')
            with self.assertRaisesRegex(AssertionError, 'batch updater'):
                self.release.verify_master_overlap(old)
            old[0]['Config']['Env'].pop()
            old[1]['Config']['Env'][0] = 'NODE_TYPE=master'
            with self.assertRaisesRegex(AssertionError, 'node role'):
                self.release.verify_master_overlap(old)

    def test_clone_uses_proven_network_order_and_removes_secret_envfile(self):
        source = {
            'Config': {'Entrypoint': ['/new-api'], 'Cmd': ['--log-dir', '/logs'],
                       'Env': ['MEMORY_CACHE_ENABLED=false', 'SQL_DSN=private-dsn', 'NODE_NAME=old-master'],
                       'User': '10001:10001', 'WorkingDir': '/data'},
            'HostConfig': {'PortBindings': {}, 'Privileged': False, 'ReadonlyRootfs': True,
                           'CapDrop': ['ALL'], 'SecurityOpt': ['no-new-privileges:true'],
                           'Tmpfs': {'/tmp': 'rw,size=64m'}},
            'NetworkSettings': {'Networks': {'new-api-cn2_database': {}, 'new-api-cn2_egress': {}}},
            'Mounts': [{'Type': 'bind', 'Source': '/srv/test-data', 'Destination': '/data', 'RW': True}],
        }
        captured_env = {}

        def fake_run(args, *unused, **kwargs):
            if args[:2] == ['docker', 'create']:
                envfile = pathlib.Path(args[args.index('--env-file') + 1])
                captured_env.update(line.split('=', 1) for line in envfile.read_text().splitlines())
                return ('c' * 64).encode()
            return b''

        with tempfile.TemporaryDirectory() as temp, \
                mock.patch.object(self.release, 'root', pathlib.Path(temp)), \
                mock.patch.object(self.release, 'run', side_effect=fake_run) as run:
            created = {}
            self.release.clone_application(source, 'candidate', 'redis://:test@new-api-model-routing-redis:6379/0', created)
            args = [call.args[0] for call in run.call_args_list]
            self.assertEqual(['docker', 'create'], args[0][:2])
            self.assertIn('new-api-cn2_database', args[0])
            self.assertEqual(['docker', 'network', 'connect', '--gw-priority', '1', 'new-api-cn2_egress', 'candidate'], args[1])
            self.assertEqual(['docker', 'network', 'connect', 'new-api-model-routing', 'candidate'], args[2])
            self.assertEqual(['docker', 'start', 'candidate'], args[3])
            self.assertFalse((pathlib.Path(temp) / 'candidate.env').exists())
            self.assertEqual('c' * 64, created['candidate'])
            self.assertEqual('candidate', captured_env['NODE_NAME'])

    def test_clone_removes_partial_secret_envfile_after_write_failure(self):
        source = {
            'Config': {'Entrypoint': ['/new-api'], 'Env': ['MEMORY_CACHE_ENABLED=false', 'SQL_DSN=private-dsn']},
            'HostConfig': {'PortBindings': {}, 'Privileged': False},
            'NetworkSettings': {'Networks': {'new-api-cn2_database': {}, 'new-api-cn2_egress': {}}},
            'Mounts': [],
        }
        original_write = pathlib.Path.write_text

        def partial_write(path, value, *args, **kwargs):
            original_write(path, value[:20], *args, **kwargs)
            raise OSError('disk write failed')

        with tempfile.TemporaryDirectory() as temp, \
                mock.patch.object(self.release, 'root', pathlib.Path(temp)), \
                mock.patch.object(self.release.pathlib.Path, 'write_text', autospec=True, side_effect=partial_write), \
                mock.patch.object(self.release, 'run') as run:
            with self.assertRaisesRegex(OSError, 'disk write failed'):
                self.release.clone_application(source, 'candidate', 'redis://:test@new-api-model-routing-redis:6379/0', {})
            self.assertFalse((pathlib.Path(temp) / 'candidate.env').exists())
            run.assert_not_called()

    def test_cleanup_never_stops_a_container_with_a_reused_name(self):
        with mock.patch.object(self.release, 'inspect', return_value=[{'Id': 'other', 'State': {'Running': True}}]), \
                mock.patch.object(self.release, 'run') as run:
            self.assertFalse(self.release.stop_owned_container('candidate', 'ours', 60, True))
            run.assert_not_called()
        with mock.patch.object(self.release, 'inspect', side_effect=[
                [{'Id': 'ours', 'State': {'Running': True}}],
                [{'Id': 'ours', 'State': {'Running': False}}]]), \
                mock.patch.object(self.release, 'run') as run:
            self.assertTrue(self.release.stop_owned_container('candidate', 'ours', 60, True))
            self.assertEqual(['docker', 'stop', '-t', '60', 'ours'], run.call_args_list[-1].args[0])

    def test_rollback_restores_old_route_even_if_candidate_is_missing(self):
        old, new = self.nodes()
        before = ('header {\nX-Newapi-Release "' + '1' * 40 + '"\n}').encode()
        with tempfile.TemporaryDirectory() as temp:
            folder = pathlib.Path(temp)
            (folder / 'stage.json').write_text(__import__('json').dumps({**self.stage, 'revision': REVISION, 'image': IMAGE}))
            (folder / 'before-private.json').write_text(__import__('json').dumps([
                {**node, 'HostConfig': {'RestartPolicy': {'Name': 'unless-stopped'}}} for node in old]))
            (folder / 'Caddyfile.before').write_bytes(before)
            (folder / 'Caddyfile.after').write_bytes(before + b' new')
            live_caddy = folder / 'Caddyfile.live'
            live_caddy.write_bytes(before + b' new')

            def inspect(names):
                if names == self.release.old_names:
                    return old
                raise AssertionError(names)

            with mock.patch.object(self.release, 'root', folder), \
                    mock.patch.object(self.release, 'caddy', live_caddy), \
                    mock.patch.object(self.release, 'inspect', side_effect=inspect), \
                    mock.patch.object(self.release, 'run') as run, \
                    mock.patch.object(self.release, 'healthy'), \
                    mock.patch.object(self.release, 'reload_routes'), \
                    mock.patch.object(self.release, 'public_checks'), \
                    mock.patch.object(self.release, 'owned_candidates', return_value=({self.release.new_names[0]: 'new-master'}, [])), \
                    mock.patch.object(self.release, 'stop_owned_container', return_value=True) as stop:
                self.release.rollback()
                stop.assert_called_once_with(self.release.new_names[0], 'new-master', 120, True)
            report = __import__('json').loads((folder / 'rollback.json').read_text())
            self.assertEqual('complete', report['status'])
            self.assertEqual([self.release.new_names[1]], report['confirmed_absent_candidates'])

    def test_rollback_reports_unresolved_candidate_after_restoring_old_route(self):
        old, _ = self.nodes()
        before = ('X-Newapi-Release "' + '1' * 40 + '"').encode()
        with tempfile.TemporaryDirectory() as temp:
            folder = pathlib.Path(temp)
            (folder / 'stage.json').write_text(__import__('json').dumps({**self.stage, 'revision': REVISION, 'image': IMAGE}))
            (folder / 'before-private.json').write_text(__import__('json').dumps([
                {**node, 'HostConfig': {'RestartPolicy': {'Name': 'unless-stopped'}}} for node in old]))
            (folder / 'Caddyfile.before').write_bytes(before)
            (folder / 'Caddyfile.after').write_bytes(before + b' new')
            live_caddy = folder / 'Caddyfile.live'
            live_caddy.write_bytes(before + b' new')

            with mock.patch.object(self.release, 'root', folder), \
                    mock.patch.object(self.release, 'caddy', live_caddy), \
                    mock.patch.object(self.release, 'inspect', return_value=old), \
                    mock.patch.object(self.release, 'run'), \
                    mock.patch.object(self.release, 'healthy'), \
                    mock.patch.object(self.release, 'reload_routes') as reload_routes, \
                    mock.patch.object(self.release, 'public_checks'), \
                    mock.patch.object(self.release, 'owned_candidates', return_value=({}, [self.release.new_names[0]])):
                with self.assertRaisesRegex(RuntimeError, 'Rollback incomplete'):
                    self.release.rollback()
                reload_routes.assert_called_once_with(before)
            report = __import__('json').loads((folder / 'rollback.json').read_text())
            self.assertEqual('partial', report['status'])
            self.assertEqual([self.release.new_names[0]], report['unresolved_candidates'])

    def test_rollback_restores_route_when_watchdog_write_was_partial(self):
        old, _ = self.nodes()
        before = ('X-Newapi-Release "' + '1' * 40 + '"').encode()
        with tempfile.TemporaryDirectory() as temp:
            folder = pathlib.Path(temp)
            (folder / 'stage.json').write_text(__import__('json').dumps({**self.stage, 'revision': REVISION, 'image': IMAGE}))
            (folder / 'before-private.json').write_text(__import__('json').dumps([
                {**node, 'HostConfig': {'RestartPolicy': {'Name': 'unless-stopped'}}} for node in old]))
            (folder / 'Caddyfile.before').write_bytes(before)
            (folder / 'Caddyfile.after').write_bytes(before + b' new')
            (folder / 'watchdog.before').write_bytes(b'old watchdog')
            (folder / 'watchdog.after').write_bytes(b'new watchdog')
            live_caddy = folder / 'Caddyfile.live'
            live_caddy.write_bytes(before + b' new')
            live_watchdog = folder / 'watchdog.live'
            live_watchdog.write_bytes(b'new watch')  # Partial write by interrupted switch.

            with mock.patch.object(self.release, 'root', folder), \
                    mock.patch.object(self.release, 'caddy', live_caddy), \
                    mock.patch.object(self.release, 'watchdog', live_watchdog), \
                    mock.patch.object(self.release, 'inspect', return_value=old), \
                    mock.patch.object(self.release, 'run'), \
                    mock.patch.object(self.release, 'healthy'), \
                    mock.patch.object(self.release, 'reload_routes') as reload_routes, \
                    mock.patch.object(self.release, 'public_checks'), \
                    mock.patch.object(self.release, 'owned_candidates', return_value=({}, [])):
                with self.assertRaisesRegex(RuntimeError, 'Rollback incomplete'):
                    self.release.rollback()
                reload_routes.assert_called_once_with(before)
            report = __import__('json').loads((folder / 'rollback.json').read_text())
            self.assertEqual('partial', report['status'])
            self.assertIn('watchdog', report['unresolved_configuration'])

    def test_stage_final_validation_failure_stops_only_created_candidates(self):
        old, new = self.nodes()
        old_caddy = (' '.join(name + ':3000' for name in self.release.old_names) +
                     ' X-Newapi-Release "' + '1' * 40 + '"').encode()
        raw_backup = b'CREATE TABLE `test` (`id` int);\n'
        with tempfile.TemporaryDirectory() as temp:
            folder = pathlib.Path(temp)
            caddy = folder / 'Caddyfile'
            caddy.write_bytes(old_caddy)
            watchdog = folder / 'watchdog.py'
            watchdog.write_text('old watchdog')

            def fake_run(args, *unused, **kwargs):
                if args[:3] == ['docker', 'image', 'inspect']:
                    return __import__('json').dumps([{'Architecture': 'amd64', 'Config': {
                        'Labels': {'org.opencontainers.image.revision': REVISION}}}]).encode()
                if args[:2] == ['docker', 'exec']:
                    return raw_backup
                raise AssertionError(args)

            def fake_inspect(names):
                if names == self.release.old_names:
                    return old
                if names == self.release.auth_names:
                    return []
                if names == self.release.new_names:
                    return new
                raise AssertionError(names)

            def fake_sql(statement):
                return '0' if 'type=57' in statement else '1' if 'COUNT(*)' in statement else ''

            created = ['c' * 64, 'd' * 64]

            def fake_clone(source, name, connection, created_ids):
                created_ids[name] = created[len(created_ids)]

            def introduce_drift(connection):
                caddy.write_bytes(b'concurrent operator edit')

            with mock.patch.object(self.release, 'root', folder / 'release'), \
                    mock.patch.object(self.release, 'caddy', caddy), \
                    mock.patch.object(self.release, 'watchdog', watchdog), \
                    mock.patch.object(self.release.subprocess, 'run', return_value=mock.Mock(returncode=1)), \
                    mock.patch.object(self.release, 'inspect', side_effect=fake_inspect), \
                    mock.patch.object(self.release, 'run', side_effect=fake_run), \
                    mock.patch.object(self.release, 'sql', side_effect=fake_sql), \
                    mock.patch.object(self.release, 'rehearse'), \
                    mock.patch.object(self.release, 'clone_application', side_effect=fake_clone), \
                    mock.patch.object(self.release, 'healthy'), \
                    mock.patch.object(self.release, 'verify_candidate_network'), \
                    mock.patch.object(self.release, 'verify_shared_redis', side_effect=introduce_drift), \
                    mock.patch.object(self.release, 'stop_owned_container', return_value=True) as stop, \
                    mock.patch.object(self.release.pathlib.Path, 'read_text', return_value='MODEL_ROUTING_REDIS_URL=redis://test'):
                with self.assertRaises(AssertionError):
                    self.release.stage()
            self.assertEqual([(self.release.new_names[0], created[0]),
                              (self.release.new_names[1], created[1])],
                             [(call.args[0], call.args[1]) for call in stop.call_args_list])
            self.assertFalse((folder / 'release' / 'stage.json').exists())

    def test_stage_record_failure_reports_unstopped_candidate(self):
        old, new = self.nodes()
        created = ['c' * 64, 'd' * 64]
        for obj, container_id in zip(new, created):
            obj['Id'] = container_id
        old_caddy = (' '.join(name + ':3000' for name in self.release.old_names) +
                     ' X-Newapi-Release "' + '1' * 40 + '"').encode()
        raw_backup = b'CREATE TABLE `test` (`id` int);\n'
        with tempfile.TemporaryDirectory() as temp:
            folder = pathlib.Path(temp)
            caddy = folder / 'Caddyfile'
            caddy.write_bytes(old_caddy)
            watchdog = folder / 'watchdog.py'
            watchdog.write_text('old watchdog')

            def fake_run(args, *unused, **kwargs):
                if args[:3] == ['docker', 'image', 'inspect']:
                    return __import__('json').dumps([{'Architecture': 'amd64', 'Config': {
                        'Labels': {'org.opencontainers.image.revision': REVISION}}}]).encode()
                if args[:2] == ['docker', 'exec']:
                    return raw_backup
                raise AssertionError(args)

            def fake_inspect(names):
                return (old if names == self.release.old_names else [] if names == self.release.auth_names
                        else new if names == self.release.new_names else None)

            def fake_sql(statement):
                return '0' if 'type=57' in statement else '1' if 'COUNT(*)' in statement else ''

            def fake_clone(source, name, connection, created_ids):
                created_ids[name] = created[len(created_ids)]

            original_save = self.release.save

            def fail_final_save(name, value):
                if name == 'stage.json':
                    raise OSError('stage record write failed')
                original_save(name, value)

            with mock.patch.object(self.release, 'root', folder / 'release'), \
                    mock.patch.object(self.release, 'caddy', caddy), \
                    mock.patch.object(self.release, 'watchdog', watchdog), \
                    mock.patch.object(self.release.subprocess, 'run', return_value=mock.Mock(returncode=1)), \
                    mock.patch.object(self.release, 'inspect', side_effect=fake_inspect), \
                    mock.patch.object(self.release, 'run', side_effect=fake_run), \
                    mock.patch.object(self.release, 'sql', side_effect=fake_sql), \
                    mock.patch.object(self.release, 'rehearse'), \
                    mock.patch.object(self.release, 'clone_application', side_effect=fake_clone), \
                    mock.patch.object(self.release, 'healthy'), \
                    mock.patch.object(self.release, 'verify_candidate_network'), \
                    mock.patch.object(self.release, 'verify_shared_redis'), \
                    mock.patch.object(self.release, 'save', side_effect=fail_final_save), \
                    mock.patch.object(self.release, 'stop_owned_container', side_effect=[True, False]) as stop, \
                    mock.patch.object(self.release.pathlib.Path, 'read_text', return_value='MODEL_ROUTING_REDIS_URL=redis://test'):
                with self.assertRaisesRegex(RuntimeError, 'candidate cleanup incomplete'):
                    self.release.stage()
            self.assertEqual(2, stop.call_count)
            self.assertFalse((folder / 'release' / 'stage.json').exists())

    def test_rehearsal_drops_temporary_account_if_network_setup_fails(self):
        statements = []

        def sql(statement):
            statements.append(statement)
            return ''

        def run(args, *unused, **kwargs):
            if args[:3] == ['docker', 'network', 'create']:
                raise RuntimeError('network setup failed')
            return b''

        with tempfile.TemporaryDirectory() as temp, \
                mock.patch.object(self.release, 'root', pathlib.Path(temp)), \
                mock.patch.object(self.release, 'sql', side_effect=sql), \
                mock.patch.object(self.release, 'run', side_effect=run), \
                mock.patch.object(self.release.subprocess, 'run') as process:
            process.return_value.returncode = 1
            with self.assertRaisesRegex(RuntimeError, 'network setup failed'):
                self.release.rehearse('modelroute_verify_aaaaaaaaaaaa')
            self.assertTrue(any(statement.startswith('DROP USER') for statement in statements))
            self.assertFalse((pathlib.Path(temp) / 'rehearsal.env').exists())

    def test_unknown_candidate_inspection_is_not_reported_as_clean_rollback(self):
        error = mock.Mock(returncode=1, stdout=b'', stderr=b'Cannot connect to Docker daemon')
        absent = mock.Mock(returncode=1, stdout=b'', stderr=b'Error: No such object')
        with mock.patch.object(self.release.subprocess, 'run', side_effect=[error, absent]):
            owned, unresolved = self.release.owned_candidates(self.stage)
        self.assertFalse(owned)
        self.assertEqual([self.release.new_names[0]], unresolved)

    def test_rehearsal_cleanup_timeout_still_drops_account(self):
        statements = []

        def sql(statement):
            statements.append(statement)
            return ''

        def run(args, *unused, **kwargs):
            if args[:2] == ['docker', 'run']:
                return ('d' * 64).encode()
            if args[:2] == ['docker', 'stop']:
                raise self.release.subprocess.TimeoutExpired(args, 15)
            return b''

        with tempfile.TemporaryDirectory() as temp, \
                mock.patch.object(self.release, 'root', pathlib.Path(temp)), \
                mock.patch.object(self.release, 'sql', side_effect=sql), \
                mock.patch.object(self.release, 'run', side_effect=run), \
                mock.patch.object(self.release, 'ledger', return_value=[]), \
                mock.patch.object(self.release, 'schema', return_value='same'), \
                mock.patch.object(self.release, 'healthy', side_effect=RuntimeError('health failed')), \
                mock.patch.object(self.release, 'inspect', return_value=[{'Id': 'd' * 64, 'State': {'Running': True}}]):
            with self.assertRaisesRegex(RuntimeError, 'Rehearsal cleanup incomplete'):
                self.release.rehearse('modelroute_verify_aaaaaaaaaaaa')
            self.assertTrue(any(statement.startswith('DROP USER') for statement in statements))
            self.assertFalse((pathlib.Path(temp) / 'rehearsal.env').exists())

    def test_rehearsal_inspect_failure_cannot_claim_clean_cleanup(self):
        statements = []

        def sql(statement):
            statements.append(statement)
            return ''

        def run(args, *unused, **kwargs):
            if args[:2] == ['docker', 'run']:
                return ('d' * 64).encode()
            return b''

        with tempfile.TemporaryDirectory() as temp, \
                mock.patch.object(self.release, 'root', pathlib.Path(temp)), \
                mock.patch.object(self.release, 'sql', side_effect=sql), \
                mock.patch.object(self.release, 'run', side_effect=run), \
                mock.patch.object(self.release, 'ledger', return_value=[]), \
                mock.patch.object(self.release, 'schema', return_value='same'), \
                mock.patch.object(self.release, 'healthy', side_effect=RuntimeError('health failed')), \
                mock.patch.object(self.release, 'inspect', side_effect=RuntimeError('inspect failed')):
            with self.assertRaisesRegex(RuntimeError, 'Rehearsal cleanup incomplete'):
                self.release.rehearse('modelroute_verify_aaaaaaaaaaaa')
            self.assertTrue(any(statement.startswith('DROP USER') for statement in statements))


if __name__ == '__main__':
    unittest.main()
