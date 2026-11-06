#!/usr/bin/env python3
"""Check the Git index against local secrets; reports paths/reasons, never values."""
from pathlib import Path
import re
import shlex
import subprocess

ROOT=Path(__file__).resolve().parents[1]

def git(*args):return subprocess.check_output(['git',*args],cwd=ROOT)

paths=[p for p in git('diff','--cached','--name-only','-z').decode().split('\0') if p]
values=[]
secret_dir=ROOT/'secrets'
if secret_dir.exists():
    for file in secret_dir.iterdir():
        if not file.is_file():continue
        text=file.read_text().strip()
        if len(text)>=16:values.append(text.encode())
        for line in text.splitlines():
            if '=' in line and len(line.split('=',1)[1])>=16:
                values.append(line.split('=',1)[1].encode())
env=ROOT.parent/'serverbot/.env'
if env.exists():
    for line in env.read_text().splitlines():
        if '=' not in line or line.lstrip().startswith('#'):continue
        key,value=line.split('=',1);key=key.strip()
        # A *_SECRET_FILE value is a documented filename, not a credential.
        if key.endswith('_FILE'):continue
        if re.search('TOKEN|PASSWORD|SECRET|ALLOWED_ID',key):
            value=' '.join(shlex.split(value,comments=True))
            if len(value)>=5:values.append(value.encode())
issues=[]
for path in paths:
    if any(part in Path(path).parts for part in ['secrets','backups','data','.venv','__pycache__']):
        issues.append((path,'excluded runtime path'))
    if Path(path).name=='.env' or path.endswith(('.sqlite','.sqlite3','.tar.gz','.dump')):
        issues.append((path,'runtime or secret file'))
    data=git('show',':'+path)
    if any(value in data for value in values):issues.append((path,'matches a local credential or private owner ID'))
    if re.search(rb'-----BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY-----|\b\d{8,12}:[A-Za-z0-9_-]{35}\b',data):
        issues.append((path,'credential pattern'))
if issues:
    for path,reason in issues:print(path+': '+reason)
    raise SystemExit('Secret audit failed; do not commit')
print(f'Staged secret audit passed: {len(paths)} files, no local credential matches or runtime datasets')
