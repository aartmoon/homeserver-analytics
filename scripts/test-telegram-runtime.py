#!/usr/bin/env python3
"""Explicit integration test: sends a test problem and recovery to the existing owner.
No production target is stopped. Uses Grafana -> webhook -> existing bot.
"""
import base64
from copy import deepcopy
import datetime
import json
from pathlib import Path
import sqlite3
import time
import urllib.request
import yaml
ROOT=Path(__file__).resolve().parents[1]
OP=urllib.request.build_opener(urllib.request.ProxyHandler({}))
AUTH='Basic '+base64.b64encode(('admin:'+(ROOT/'secrets/grafana_admin_password').read_text().strip()).encode()).decode()
CONFIG=ROOT/'grafana/provisioning/alerting/runtime-telegram-test.yaml'
METRIC=ROOT/'textfile/runtime-telegram-test.prom'
POLICY=ROOT/'grafana/provisioning/alerting/notifications.yaml'
INBOX=ROOT/'data/serverbot/monitoring-inbox.sqlite3'
OBS=[]

def call(path,method='GET'):
    req=urllib.request.Request('http://127.0.0.1:3001'+path,method=method,headers={'Authorization':AUTH})
    with OP.open(req,timeout=10) as response:return json.load(response)

def reload():call('/api/admin/provisioning/alerting/reload','POST')

def state():
    for group in call('/api/prometheus/grafana/api/v1/rules')['data']['groups']:
        for rule in group['rules']:
            if rule['name']=='TelegramDeliverySelfTest':return rule.get('state'),rule['health']
    return None,None

def delivered(kind):
    with sqlite3.connect('file:'+str(INBOX)+'?mode=ro',uri=True) as db:
        return db.execute('SELECT count(*) FROM events WHERE delivered=1 AND created>=? AND text LIKE ? AND text LIKE ?',
                          (START,'%Проверка: TelegramDeliverySelfTest%','%'+kind+'%')).fetchone()[0]

def wait(predicate,label,timeout=210):
    deadline=time.monotonic()+timeout
    while time.monotonic()<deadline:
        if predicate():
            entry={'time':datetime.datetime.now(datetime.timezone.utc).isoformat(),'step':label}
            OBS.append(entry);print(entry['time'],label,flush=True);return
        time.sleep(3)
    raise RuntimeError('Deadline waiting for '+label)

def metric(value):
    tmp=METRIC.with_suffix('.tmp');tmp.write_text('monitoring_telegram_selftest{project="test",service="telegram-selftest",environment="test",instance="selftest"} '+str(value)+'\n');tmp.chmod(0o644);tmp.replace(METRIC)

if CONFIG.exists():raise SystemExit('Existing Telegram self-test config; inspect before running')
START=time.time()
base=yaml.safe_load((ROOT/'grafana/provisioning/alerting/rules.yaml').read_text())['groups'][0]['rules'][0]
rule=deepcopy(base);rule.update(uid='telegram-delivery-test',title='TelegramDeliverySelfTest',labels={'project':'test','service':'telegram-selftest','environment':'test','instance':'selftest','severity':'warning'},annotations={'summary':'Test Telegram delivery; no production outage'})
rule['for']='10s';rule['data'][0]['model']['expr']='monitoring_telegram_selftest'
policy=yaml.safe_load(POLICY.read_text());production=deepcopy(policy)
test_policy=deepcopy(production)
test_policy['policies'][0]['routes']=[{'receiver':'serverbot','object_matchers':[['project','=','test']],'group_wait':'0s','group_interval':'10s','repeat_interval':'4h'}] + production['policies'][0].get('routes',[])
try:
    metric(0)
    CONFIG.write_text(yaml.safe_dump({'apiVersion':1,'groups':[{'orgId':1,'name':'Telegram delivery test','folder':'Monitoring','interval':'10s','rules':[rule]}]},sort_keys=False));CONFIG.chmod(0o644)
    POLICY.write_text(yaml.safe_dump(test_policy,sort_keys=False));POLICY.chmod(0o644);reload()
    wait(lambda:state()==('inactive','ok'),'test rule normal')
    metric(1);wait(lambda:state()==('firing','ok'),'test rule firing')
    wait(lambda:delivered('Проблема')>=1,'Telegram accepted test problem')
    metric(0);wait(lambda:state()==('inactive','ok'),'test rule recovered')
    wait(lambda:delivered('Восстановление')>=1,'Telegram accepted test recovery')
    print('REAL GRAFANA -> EXISTING BOT -> TELEGRAM PROBLEM AND RECOVERY VERIFIED',flush=True)
finally:
    CONFIG.write_text(yaml.safe_dump({'apiVersion':1,'deleteRules':[{'orgId':1,'uid':'telegram-delivery-test'}]}));CONFIG.chmod(0o644)
    POLICY.write_text(yaml.safe_dump(production,sort_keys=False));POLICY.chmod(0o644)
    reload();CONFIG.unlink(missing_ok=True);METRIC.unlink(missing_ok=True)
    (ROOT/'reports/runtime-telegram-test.json').write_text(json.dumps(OBS,indent=2)+'\n')
