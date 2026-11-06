#!/usr/bin/env python3
"""Create a local-only temporary Grafana rule and exercise firing/recovery.
Does not stop production services. Temporarily mutes only project=test notifications.
"""
import base64
from copy import deepcopy
import datetime
import json
from pathlib import Path
import time
import urllib.request
import yaml

ROOT=Path(__file__).resolve().parents[1]
OP=urllib.request.build_opener(urllib.request.ProxyHandler({}))
SECRET=(ROOT/'secrets/grafana_admin_password').read_text().strip()
AUTH='Basic '+base64.b64encode(('admin:'+SECRET).encode()).decode()
CONFIG=ROOT/'grafana/provisioning/alerting/runtime-selftest.yaml'
METRIC=ROOT/'textfile/runtime-selftest.prom'
POLICY=ROOT/'grafana/provisioning/alerting/notifications.yaml'
OBS=[]


def call(path,method='GET'):
    req=urllib.request.Request('http://127.0.0.1:3001'+path,method=method,headers={'Authorization':AUTH})
    with OP.open(req,timeout=10) as response:return json.load(response)


def reload():call('/api/admin/provisioning/alerting/reload','POST')


def state():
    data=call('/api/prometheus/grafana/api/v1/rules')
    for group in data['data']['groups']:
        for rule in group['rules']:
            if rule['name']=='RuntimeSelfTest':return rule.get('state'),rule['health']
    return None,None


def wait_for(wanted):
    deadline=time.monotonic()+180
    last=None
    while time.monotonic()<deadline:
        current,health=state()
        if (current,health)!=last:
            stamp=datetime.datetime.now(datetime.timezone.utc).isoformat()
            OBS.append({'time':stamp,'state':current,'health':health})
            print(stamp,current,health,flush=True);last=(current,health)
        if current==wanted and health=='ok':return
        time.sleep(5)
    raise RuntimeError('Self-test state deadline exceeded')


def metric(value):
    tmp=METRIC.with_suffix('.tmp')
    tmp.write_text('monitoring_runtime_selftest{project="test",service="selftest",environment="test",instance="selftest"} '+str(value)+'\n')
    tmp.chmod(0o644);tmp.replace(METRIC)


if CONFIG.exists():raise SystemExit('Self-test provisioning already exists; inspect first')
base=yaml.safe_load((ROOT/'grafana/provisioning/alerting/rules.yaml').read_text())['groups'][0]['rules'][0]
rule=deepcopy(base)
rule.update(uid='runtime-selftest',title='RuntimeSelfTest',labels={'project':'test','service':'selftest','environment':'test','instance':'selftest','severity':'warning'},annotations={'summary':'Local runtime test, no notifications'})
rule['for']='30s';rule['data'][0]['model']['expr']='monitoring_runtime_selftest'
original_policy=yaml.safe_load(POLICY.read_text())
muted_policy=deepcopy(original_policy)
root_policy=muted_policy['policies'][0]
root_policy['routes']=[{'receiver':root_policy['receiver'],
    'object_matchers':[['project','=','test']],
    'mute_time_intervals':['delivery-disabled']}] + root_policy.get('routes',[])
try:
    POLICY.write_text(yaml.safe_dump(muted_policy,sort_keys=False));POLICY.chmod(0o644)
    metric(0)
    CONFIG.write_text(yaml.safe_dump({'apiVersion':1,'groups':[{'orgId':1,'name':'Runtime selftest','folder':'Monitoring','interval':'10s','rules':[rule]}]},sort_keys=False))
    CONFIG.chmod(0o644);reload()
    wait_for('inactive')
    metric(1);wait_for('firing')
    metric(0);wait_for('inactive')
    print('FIRING AND RECOVERY VERIFIED',flush=True)
finally:
    POLICY.write_text(yaml.safe_dump(original_policy,sort_keys=False));POLICY.chmod(0o644)
    CONFIG.write_text(yaml.safe_dump({'apiVersion':1,'deleteRules':[{'orgId':1,'uid':'runtime-selftest'}]}));CONFIG.chmod(0o644)
    reload();CONFIG.unlink(missing_ok=True);METRIC.unlink(missing_ok=True)
    (ROOT/'reports/runtime-alert-test.json').write_text(json.dumps(OBS,indent=2)+'\n')
