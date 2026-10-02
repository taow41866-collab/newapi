import datetime
import hashlib
import json
import pathlib
import re
import subprocess
import sys
import time
import urllib.request


def run(arguments):
    result = subprocess.run(arguments, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"{arguments[0]} failed ({result.returncode}): {result.stderr.strip()}")
    return result.stdout.strip()


release_name = sys.argv[1]
assert re.fullmatch(r"newapi-canvas-nav-[a-f0-9]{12}", release_name)
base = pathlib.Path("/srv/new-api/cn2-20260921")
site = base / "original-config/site"
release = site / release_name
assert (release / "index.html").is_file()
config_path = base / "Caddyfile"
original = config_path.read_text()
assert "X-Newapi-Frontend-Revision" not in original
start = original.index("    handle @frontend {")
end = original.index("\n    handle {\n        reverse_proxy new-api-modelroute", start)
frontend = f'''    handle @frontend {{
        header Cache-Control "no-cache"
        header X-Newapi-Frontend-Revision "{release_name}"
        root * /srv/{release_name}
        @frontend_file file {{path}}
        handle @frontend_file {{
            file_server
        }}
        @embedded_frontend_asset path /static/*
        handle @embedded_frontend_asset {{
            reverse_proxy new-api-modelroute-a5a347f1-master:3000
        }}
        handle {{
            rewrite * /index.html
            file_server
        }}
    }}
'''
candidate = site / ".frontend-Caddyfile-candidate"
candidate.write_text(original[:start] + frontend + original[end:])
run(["docker", "exec", "new-api-cn2-caddy", "caddy", "validate", "--config", "/srv/.frontend-Caddyfile-candidate", "--adapter", "caddyfile"])
stamp = datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).strftime("%Y%m%d-%H%M%S")
backup = base / "canvas-navigation" / stamp
backup.mkdir(parents=True, mode=0o700)
(backup / "Caddyfile.before").write_text(original)
assert config_path.read_text() == original
try:
    config_path.write_text(candidate.read_text())
    run(["docker", "kill", "--signal", "USR1", "new-api-cn2-caddy"])
    for attempt in range(15):
        with urllib.request.urlopen("https://lpss.online/channels", timeout=15) as response:
            if response.headers.get("X-Newapi-Frontend-Revision") == release_name:
                break
        time.sleep(1)
    else:
        raise RuntimeError("New frontend route did not activate")
    for url in ["https://lpss.online/api/status", "https://lpss.online/healthz", "https://lpss.online/canvas/canvas", "https://lpss.online/static/js/index.6c7e7ff400.js"]:
        with urllib.request.urlopen(url, timeout=15) as response:
            assert response.status == 200
except Exception:
    config_path.write_text(original)
    run(["docker", "kill", "--signal", "USR1", "new-api-cn2-caddy"])
    raise
receipt = {
    "timestamp": stamp,
    "release": str(release),
    "backup": str(backup),
    "caddy_sha256": hashlib.sha256(config_path.read_bytes()).hexdigest(),
    "html_sha256": hashlib.sha256((release / "index.html").read_bytes()).hexdigest(),
    "backend_containers_restarted": False,
    "previous_embedded_assets_preserved": True,
}
(backup / "receipt.json").write_text(json.dumps(receipt, indent=2))
print(json.dumps(receipt, indent=2))
