#!/usr/bin/env python3
"""Prepare private local files after clone; never print or overwrite existing passwords.
Does not create a PostgreSQL role, modify the bot, or send notifications.
"""
from pathlib import Path
import os
import secrets

root=Path(__file__).resolve().parents[1]
private=root/'secrets';private.mkdir(mode=0o700,exist_ok=True);private.chmod(0o700)
for name in ['grafana_admin_password','postgres_password','grafana_webhook','kuma_webhook']:
    target=private/name
    if not target.exists():
        fd=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        with os.fdopen(fd,'w') as stream:stream.write(secrets.token_urlsafe(48))
    target.chmod(0o600)
target=private/'grafana-webhook.env'
fd=os.open(target,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
with os.fdopen(fd,'w') as stream:
    stream.write('SERVERBOT_GRAFANA_WEBHOOK_SECRET='+(private/'grafana_webhook').read_text().strip()+'\n')
target.chmod(0o600)
for folder in ['data','data/grafana','data/container-collector','backups']:
    p=root/folder;p.mkdir(mode=0o700,parents=True,exist_ok=True);p.chmod(0o700)
for folder in ['prometheus','blackbox','grafana','textfile']:
    p=root/folder;p.mkdir(mode=0o755,exist_ok=True);p.chmod(0o755)
    for child in p.rglob('*'):child.chmod(0o755 if child.is_dir() else 0o644)
print('Local secret files and directories ready; existing passwords preserved. PostgreSQL role and bot deployment require the documented steps.')
