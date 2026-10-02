"""Use existing Git credential-manager identity without printing/storing tokens."""
import argparse
import json
import subprocess
import urllib.error
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('action', choices=['status', 'dispatch', 'run'])
parser.add_argument('--sha')
parser.add_argument('--workflow', default='ci.yml')
parser.add_argument('--run-id')
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
path = '/actions/workflows/' + args.workflow + '/runs?per_page=10&branch=color-fix-staging'
data = None
if args.action == 'dispatch':
    path = '/actions/workflows/' + args.workflow + '/dispatches'
    data = json.dumps({'ref': 'color-fix-staging'}).encode()
elif args.action == 'run':
    if not args.run_id or not args.run_id.isdigit():
        raise SystemExit('--run-id required')
    path = '/actions/runs/' + args.run_id
request = urllib.request.Request(base + path, data=data, headers={
    'Authorization': 'Bearer ' + secret, 'Accept': 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28', 'User-Agent': 'newapi-release-check'})
try:
    with urllib.request.urlopen(request, timeout=30) as response:
        body = response.read()
        if not body:
            print(json.dumps({'status': response.status, 'workflow': args.workflow, 'branch': 'color-fix-staging'}))
        else:
            payload = json.loads(body)
            rows = payload.get('workflow_runs', [payload])
            keep = ['id', 'head_sha', 'status', 'conclusion', 'html_url', 'created_at', 'name']
            print(json.dumps([{k: row.get(k) for k in keep} for row in rows
                if not args.sha or row.get('head_sha') == args.sha], indent=2))
except urllib.error.HTTPError as error:
    raise SystemExit('GitHub API returned HTTP ' + str(error.code) + '; credential not printed.')
