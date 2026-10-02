import datetime
import hashlib
import json
import pathlib
import re
import subprocess
import time
import urllib.request


def run(arguments):
    result = subprocess.run(arguments, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f"{arguments[0]} failed ({result.returncode}): {result.stderr.strip()}")
    return result.stdout.strip()


def container_env(name):
    inspected = json.loads(run(["docker", "inspect", name]))[0]
    return dict(value.split("=", 1) for value in inspected["Config"]["Env"] if "=" in value)


base = pathlib.Path("/srv/new-api/cn2-20260921")
site = base / "original-config/site"
release = site / "canvas-newapi-dab19adc"
assert (release / "index.html").is_file() and (release / "LICENSE").is_file()
config_path = base / "Caddyfile"
original = config_path.read_text()
assert "handle_path /canvas/*" not in original
marker = "    @sub2api_api path"
assert original.count(marker) == 1
assert original.count("        X-Frame-Options SAMEORIGIN\n") == 1
block = '''    @newapi_frame_policy not path /canvas /canvas/*
    header @newapi_frame_policy X-Frame-Options SAMEORIGIN
    @canvas_root path /canvas
    handle @canvas_root {
        redir /canvas/ 308
    }
    handle_path /canvas/* {
        root * /srv/canvas-newapi-dab19adc
        header {
            -X-Frame-Options
            Content-Security-Policy "frame-ancestors 'self' https://lpss.online https://hk.zhongzhuan.de5.net"
            X-Canvas-Revision "dab19adc-newapi"
            Cache-Control "no-cache"
        }
        @canvas_assets path /assets/*
        header @canvas_assets Cache-Control "public, max-age=31536000, immutable"
        try_files {path} /index.html
        file_server
    }
'''
candidate = site / ".canvas-Caddyfile-candidate"
candidate.write_text(original.replace(marker, block + marker).replace("        X-Frame-Options SAMEORIGIN\n", ""))
run(["docker", "exec", "new-api-cn2-caddy", "caddy", "validate", "--config", "/srv/.canvas-Caddyfile-candidate", "--adapter", "caddyfile"])
app_env = container_env("new-api-modelroute-a5a347f1-master")
database = app_env["SQL_DSN"].split(")/", 1)[1].split("?", 1)[0]
assert re.fullmatch(r"[A-Za-z0-9_]+", database)
password = container_env("new-api-cn2-mysql")["MYSQL_ROOT_PASSWORD"]


def sql(query):
    return run(["docker", "exec", "-e", "MYSQL_PWD=" + password, "new-api-cn2-mysql", "mysql", "-uroot", "--batch", "--skip-column-names", database, "-e", query])


old_hex = sql("SELECT HEX(value) FROM options WHERE `key`='Chats'")
with urllib.request.urlopen("https://lpss.online/api/status", timeout=15) as response:
    old_chats = json.load(response)["data"]["chats"]
assert isinstance(old_chats, list)
name = "无限画布"
assert not any(name in entry for entry in old_chats)
chat_url = "https://lpss.online/canvas/canvas#baseUrl={address}&apiKey={key}"
new_chats = old_chats + [{name: chat_url}]
new_hex = json.dumps(new_chats, ensure_ascii=False).encode().hex()
stamp = datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).strftime("%Y%m%d-%H%M%S")
backup = base / "canvas-integration" / stamp
backup.mkdir(parents=True, mode=0o700)
(backup / "Caddyfile.before").write_text(original)
(backup / "rollback.json").write_text(json.dumps({"old_chats_hex": old_hex, "new_chats_hex": new_hex, "chat_name": name, "chat_url": chat_url, "release": str(release)}, ensure_ascii=False, indent=2))
assert config_path.read_text() == original
assert sql("SELECT HEX(value) FROM options WHERE `key`='Chats'") == old_hex
config_path.write_text(candidate.read_text())
run(["docker", "kill", "--signal", "USR1", "new-api-cn2-caddy"])
try:
    for attempt in range(15):
        with urllib.request.urlopen("https://lpss.online/canvas/canvas", timeout=15) as response:
            if response.headers.get("X-Canvas-Revision") == "dab19adc-newapi":
                break
        time.sleep(1)
    else:
        raise RuntimeError("Caddy did not activate the canvas route")
    if old_hex:
        changed = sql(f"UPDATE options SET value=CONVERT(0x{new_hex} USING utf8mb4) WHERE `key`='Chats' AND BINARY value=CONVERT(0x{old_hex} USING utf8mb4); SELECT ROW_COUNT();")
        assert changed == "1", "Concurrent Chats update detected"
    else:
        sql(f"INSERT INTO options (`key`,value) VALUES ('Chats',CONVERT(0x{new_hex} USING utf8mb4));")
except Exception:
    config_path.write_text(original)
    run(["docker", "kill", "--signal", "USR1", "new-api-cn2-caddy"])
    raise
receipt = {"timestamp": stamp, "backup": str(backup), "preset_index": len(old_chats), "release": str(release), "caddy_sha256": hashlib.sha256(config_path.read_bytes()).hexdigest()}
(backup / "receipt.json").write_text(json.dumps(receipt, indent=2))
print(json.dumps(receipt, indent=2))
