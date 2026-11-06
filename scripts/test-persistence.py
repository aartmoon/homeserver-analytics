#!/usr/bin/env python3
"""Restart monitoring containers only; verify TSDB and writable Grafana/Kuma state."""
import base64
import json
from pathlib import Path
import subprocess
import time
import urllib.parse
import urllib.request
ROOT=Path(__file__).resolve().parents[1]
OP=urllib.request.build_opener(urllib.request.ProxyHandler({}))
AUTH='Basic '+base64.b64encode(('admin:'+(ROOT/'secrets/grafana_admin_password').read_text().strip()).encode()).decode()

def request(url,method='GET',body=None,auth=False):
    headers={'Content-Type':'application/json'}
    if auth:headers['Authorization']=AUTH
    req=urllib.request.Request(url,headers=headers,method=method,data=json.dumps(body).encode() if body is not None else None)
    return json.load(OP.open(req,timeout=15))

def graf(path,method='GET',body=None):return request('http://127.0.0.1:3001'+path,method,body,True)

stamp=time.time()-1
url='http://127.0.0.1:9090/api/v1/query?'+urllib.parse.urlencode({'query':'node_boot_time_seconds','time':stamp})
before=request(url)['data']['result']
assert before,'No historical host sample yet'
uid='monitoring-persistence-selftest'
marker='persistence-'+str(int(time.time()))
graf('/api/dashboards/db','POST',{'dashboard':{'id':None,'uid':uid,'title':marker,'schemaVersion':39,'panels':[]},'overwrite':False})
subprocess.run(['docker','compose','exec','-T','kuma','node','-e',"require('fs').writeFileSync('/app/data/.monitoring-persistence-selftest',process.argv[1])",marker],cwd=ROOT,check=True,capture_output=True)
try:
    subprocess.run(['docker','compose','restart'],cwd=ROOT,check=True)
    for attempt in range(60):
        try:
            assert graf('/api/dashboards/uid/'+uid)['dashboard']['title']==marker
            assert request(url)['data']['result']==before
            break
        except Exception:
            if attempt==59:raise
            time.sleep(2)
    result=subprocess.run(['docker','compose','exec','-T','kuma','node','-e',"process.stdout.write(require('fs').readFileSync('/app/data/.monitoring-persistence-selftest','utf8'))"],cwd=ROOT,check=True,capture_output=True,text=True)
    assert result.stdout==marker
    report={'verified_at':time.time(),'historical_query_time':stamp,'historical_samples':before,'grafana_writable_dashboard_persisted':True,'kuma_volume_marker_persisted':True}
    (ROOT/'reports/runtime-persistence.json').write_text(json.dumps(report,indent=2)+'\n')
    print('Historical Prometheus sample, writable Grafana dashboard and Kuma volume survived restart.',flush=True)
finally:
    graf('/api/dashboards/uid/'+uid,'DELETE')
    subprocess.run(['docker','compose','exec','-T','kuma','node','-e',"require('fs').unlinkSync('/app/data/.monitoring-persistence-selftest')"],cwd=ROOT,check=True,capture_output=True)
