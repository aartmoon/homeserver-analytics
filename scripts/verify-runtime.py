#!/usr/bin/env python3
"""Read-only checks; does not print passwords or full HTTP payloads."""
import base64
import json
from pathlib import Path
import urllib.request
root = Path(__file__).resolve().parents[1]
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

def get(url, auth=False):
    req=urllib.request.Request(url)
    if auth:
        secret=(root/'secrets/grafana_admin_password').read_text().strip()
        req.add_header('Authorization','Basic '+base64.b64encode(('admin:'+secret).encode()).decode())
    with opener.open(req,timeout=10) as r: return json.load(r)

assert get('http://127.0.0.1:3001/api/health')['database']=='ok'
targets=get('http://127.0.0.1:9090/api/v1/targets')['data']['activeTargets']
assert targets, 'No targets'
for target in targets:
    labels=target['labels']
    print(labels.get('project'), labels.get('service'), labels.get('instance'), target['health'])
assert all(x['health']=='up' for x in targets), 'Some scrapes are DOWN'
for query in ['node_memory_MemAvailable_bytes','probe_success','prometheus_tsdb_head_series','pg_up','pg_settings_max_connections','container_up','traefik_service_requests_total','traefik_service_request_duration_seconds_bucket','node_network_receive_bytes_total{device="wlp4s0"}']:
    from urllib.parse import urlencode
    result=get('http://127.0.0.1:9090/api/v1/query?'+urlencode({'query':query}))['data']['result']
    assert result, 'No series: '+query
    if query=='probe_success': assert all(float(x['value'][1])==1 for x in result),'Some HTTP probes fail'
    print(query, 'series:',len(result))
for uid in ['host','projects','project','postgres','monitoring']:
    d=get('http://127.0.0.1:3001/api/dashboards/uid/'+uid,True)
    assert d['dashboard']['uid']==uid
# Query through Grafana's provisioned datasource, not only Prometheus directly.
import time
for expr in ['1-avg(rate(node_cpu_seconds_total{mode="idle"}[5m]))','probe_success','pg_up','up','sum by(project,service) (rate(traefik_service_requests_total[5m]))']:
    payload={'from':str(int((time.time()-600)*1000)),'to':str(int(time.time()*1000)),
             'queries':[{'refId':'A','datasource':{'uid':'prometheus','type':'prometheus'},
                         'expr':expr,'instant':True,'range':False,'intervalMs':30000,'maxDataPoints':1000}]}
    req=urllib.request.Request('http://127.0.0.1:3001/api/ds/query',data=json.dumps(payload).encode(),headers={'Content-Type':'application/json'})
    secret=(root/'secrets/grafana_admin_password').read_text().strip()
    req.add_header('Authorization','Basic '+base64.b64encode(('admin:'+secret).encode()).decode())
    response=json.load(opener.open(req,timeout=15))
    result=response['results']['A']
    assert not result.get('error'), 'Grafana datasource query failed'
    assert any(frame.get('data',{}).get('values') and frame['data']['values'][-1] for frame in result.get('frames',[])), 'Grafana datasource returned no data'
    print('Grafana datasource query OK:',expr)
print('Targets, samples, five dashboard objects and Grafana datasource queries verified.')
