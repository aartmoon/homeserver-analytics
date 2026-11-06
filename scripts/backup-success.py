#!/usr/bin/env python3
"""Call ONLY after actual backup upload and verification succeeded. Not a backup job."""
import argparse
import os
from pathlib import Path
import re
import tempfile
import time
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('project')
p.add_argument('job')
a = p.parse_args()
if not all(re.fullmatch(r'[a-z][a-z0-9_-]{0,39}', x) for x in [a.project, a.job]):
    p.error('Use stable project/job names')
d = Path(__file__).resolve().parents[1] / 'textfile'
fd, temp = tempfile.mkstemp(dir=d, prefix='.backup-')
try:
    with os.fdopen(fd, 'w') as f:
        f.write('# HELP backup_last_success_timestamp_seconds Last verified successful backup Unix time\n')
        f.write('# TYPE backup_last_success_timestamp_seconds gauge\n')
        f.write(f'backup_last_success_timestamp_seconds{{project="{a.project}",service="backup",environment="production",instance="homeserver",backup_job="{a.job}"}} {time.time():.0f}\n')
        f.flush(); os.fsync(f.fileno())
    os.chmod(temp, 0o644)
    os.replace(temp, d / f'backup-{a.project}-{a.job}.prom')
finally:
    if os.path.exists(temp): os.unlink(temp)
