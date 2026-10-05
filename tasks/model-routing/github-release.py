"""Use existing Git credential-manager identity without printing/storing tokens."""
import argparse
import json
import re
import subprocess
import urllib.error
import urllib.parse
import urllib.request

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, msg, headers, newurl):
        return None

parser = argparse.ArgumentParser()
parser.add_argument('action', choices=['status', 'dispatch', 'run', 'jobs', 'logs'])
parser.add_argument('--sha')
parser.add_argument('--workflow', default='ci.yml')
parser.add_argument('--run-id')
parser.add_argument('--job-id')
parser.add_argument('--ref', default='color-fix-staging')
args = parser.parse_args()
credential = subprocess.run(['git', 'credential', 'fill'], input='protocol=https\nhost=github.com\n\n',
                            capture_output=True, text=True, timeout=30)
if credential.returncode:
    raise SystemExit('Existing Git credentials unavailable; no credentials printed.')
fields = dict(line.split('=', 1) for line in credential.stdout.splitlines() if '=' in line)
secret = fields.get('password')
if not secret:
    raise SystemExit('Existing credential helper returned no usable credential.')
base = 'https://api.github.com/repos/taow41866-collab/newapi'
path = '/actions/workflows/' + args.workflow + '/runs?per_page=10&branch=' + urllib.parse.quote(args.ref, safe='')
data = None
if args.action == 'dispatch':
    path = '/actions/workflows/' + args.workflow + '/dispatches'
    data = json.dumps({'ref': args.ref}).encode()
elif args.action in ('run', 'jobs', 'logs'):
    if not args.run_id or not args.run_id.isdigit():
        raise SystemExit('--run-id required')
    path = '/actions/runs/' + args.run_id
    if args.action == 'jobs':
        path += '/jobs'
    if args.action == 'logs':
        if not args.job_id or not args.job_id.isdigit():
            raise SystemExit('--job-id required')
        path = '/actions/jobs/' + args.job_id + '/logs'
request = urllib.request.Request(base + path, data=data, headers={
    'Authorization': 'Bearer ' + secret, 'Accept': 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'newapi-release-check'})
try:
    opener = urllib.request.build_opener(NoRedirect()) if args.action == 'logs' else urllib.request.build_opener()
    try:
        response = opener.open(request, timeout=30)
    except urllib.error.HTTPError as redirect:
        if args.action != 'logs' or redirect.code not in (301, 302, 303, 307, 308):
            raise
        location = redirect.headers.get('Location')
        if not location:
            raise
        response = urllib.request.urlopen(urllib.request.Request(location), timeout=30)
    with response:
        body = response.read()
        if not body:
            print(json.dumps({'status': response.status, 'workflow': args.workflow, 'branch': args.ref}))
        else:
            if args.action == 'logs':
                lines = body.decode(errors='replace').splitlines()
                selected = set()
                for index, line in enumerate(lines):
                    if '--- FAIL:' in line or 'Error Trace:' in line:
                        selected.update(range(max(0, index-2), min(len(lines), index+24)))
                for index in sorted(selected)[:180]:
                    print(re.sub(r'(gh[pousr]_[A-Za-z0-9]+|Bearer\s+\S+|://[^\s/]+:[^\s@]+@)', '[REDACTED]', lines[index]))
                raise SystemExit(0)
            payload = json.loads(body)
            rows = payload.get('workflow_runs', payload.get('jobs', [payload]))
            keep = ['id', 'head_sha', 'status', 'conclusion', 'html_url', 'created_at', 'name']
            if args.action == 'jobs':
                keep += ['steps']
            print(json.dumps([{k: row.get(k) for k in keep} for row in rows
                if not args.sha or row.get('head_sha') == args.sha], indent=2))
except urllib.error.HTTPError as error:
    raise SystemExit('GitHub API returned HTTP ' + str(error.code) + '; credential not printed.')
