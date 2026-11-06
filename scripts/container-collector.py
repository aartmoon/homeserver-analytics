#!/usr/bin/env python3
"""Host-only Docker metadata/statistics -> Node Exporter textfile.
No Docker socket mounted into any monitoring container. No application env logged.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
STATE = ROOT / 'data/container-collector'


def docker(*args):
    return subprocess.check_output(['docker', *args], timeout=35, stderr=subprocess.DEVNULL).decode()


def labels(project, service, instance):
    values={'project':project,'service':service,'environment':'production','instance':instance}
    return '{'+','.join(k+'='+json.dumps(v) for k,v in values.items())+'}'


def project_of(project, service):
    if project=='ruk':
        return 'infrastructure' if service in ('postgres','traefik') else 'cottontoys'
    if project=='monitoring': return 'infrastructure'
    return project


def bytes_value(value):
    match=re.fullmatch(r'([0-9.]+)\s*([A-Za-z]+)',value.strip())
    if not match: raise ValueError('Invalid size')
    units={'B':1,'kB':1000,'KB':1000,'KiB':1024,'MB':1000**2,'MiB':1024**2,'GB':1000**3,'GiB':1024**3,'TB':1000**4,'TiB':1024**4}
    return float(match[1])*units[match[2]]


def atomic(path,text,mode):
    fd,name=tempfile.mkstemp(dir=path.parent,prefix='.'+path.name)
    try:
        with os.fdopen(fd,'w') as stream:
            stream.write(text);stream.flush();os.fsync(stream.fileno())
        os.chmod(name,mode);os.replace(name,path)
    finally:
        if os.path.exists(name):os.unlink(name)


def main():
    STATE.mkdir(mode=0o700,parents=True,exist_ok=True)
    state_path=STATE/'state.json'
    previous=json.loads(state_path.read_text()) if state_path.exists() else {'cursor':time.time()-1,'containers':{},'seen':[]}
    now=time.time()
    ids=docker('ps','-aq','--filter','label=com.docker.compose.project').split()
    inspected=json.loads(docker('inspect',*ids)) if ids else []
    events=[json.loads(line) for line in docker('events','--since',str(int(previous['cursor'])-1),'--until',str(int(now)), '--filter','event=oom','--format','{{json .}}').splitlines() if line]
    stats={}
    for line in docker('stats','--no-stream','--format','{{json .}}').splitlines():
        row=json.loads(line);stats[row['Name']]=row
    records=previous['containers']
    for row in records.values():row['up']=0
    for item in inspected:
        policy=item['HostConfig']['RestartPolicy']['Name']
        if policy not in ('always','unless-stopped','on-failure'): continue
        meta=item['Config']['Labels'];pr=meta['com.docker.compose.project'];service=meta['com.docker.compose.service']
        if pr not in ('ruk','talk','monitoring'):continue
        name=item['Name'].removeprefix('/')
        rec=records.setdefault(name,{'project':project_of(pr,service),'service':service,'id':item['Id'],'restart_snapshot':0,'restarts':0,'oom':0})
        restart=item['RestartCount']
        delta=restart-rec['restart_snapshot'] if rec['id']==item['Id'] else restart
        rec['restarts']+=max(0,delta);rec['restart_snapshot']=restart;rec['id']=item['Id']
        rec['up']=int(item['State']['Running'] and not item['State'].get('Restarting',False))
    seen=set(previous['seen'])
    for event in events:
        key=str(event.get('timeNano'))+':'+event.get('id',event.get('Actor',{}).get('ID',''))
        if key in seen:continue
        seen.add(key)
        name=event.get('Actor',{}).get('Attributes',{}).get('name')
        if name in records:records[name]['oom']+=1
    output=[]
    for metric,kind in [('container_up','gauge'),('container_restarts_total','counter'),('container_oom_events_total','counter'),('container_memory_working_set_bytes','gauge'),('container_cpu_usage_cores','gauge')]:
        output.append(f'# TYPE {metric} {kind}')
    for name,rec in records.items():
        lab=labels(rec['project'],rec['service'],name)
        for metric,key in [('container_up','up'),('container_restarts_total','restarts'),('container_oom_events_total','oom')]:output.append(f'{metric}{lab} {rec[key]}')
        if name in stats:
            row=stats[name]
            output.append(f'container_memory_working_set_bytes{lab} {bytes_value(row["MemUsage"].split("/")[0])}')
            output.append(f'container_cpu_usage_cores{lab} {float(row["CPUPerc"].rstrip("%"))/100}')
    output.append('container_collector_last_success_timestamp_seconds'+labels('infrastructure','container-collector','homeserver')+f' {now}')
    atomic(state_path,json.dumps({'cursor':now,'containers':records,'seen':sorted(seen)[-10000:]}),0o600)
    atomic(ROOT/'textfile/containers.prom','\n'.join(output)+'\n',0o644)


if __name__=='__main__':
    try:main()
    except Exception:
        # Never dump Docker inspect/env or SDK error text into logs.
        raise SystemExit('Container collector failed; last successful metrics retained') from None
