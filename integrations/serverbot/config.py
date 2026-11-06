"""Small, dependency-free configuration loader. Never executes .env files."""
import os
import shlex
from pathlib import Path

ROOT = Path(__file__).resolve().parent


def env_file(path):
    result = {}
    if path.exists():
        for line in path.read_text().splitlines():
            line = line.strip().removeprefix('export ')
            if not line or line.startswith('#') or '=' not in line:
                continue
            key, value = line.split('=', 1)
            parts = shlex.split(value, comments=True)
            result[key.strip()] = ' '.join(parts)
    return result


for _key, _value in env_file(ROOT / '.env').items():
    os.environ.setdefault(_key, _value)


class Config:
    def __init__(self):
        self.root = ROOT
        self.data = ROOT / 'data'
        self.data.mkdir(mode=0o700, exist_ok=True)
        self.data.chmod(0o700)
        self.owner = int(os.environ.get('TG_ALLOWED_ID', '0'))
        self.token = os.environ.get('TG_BOT_TOKEN', '')
        self.proxy = os.environ.get('TG_PROXY', 'socks5://127.0.0.1:1080') or None
