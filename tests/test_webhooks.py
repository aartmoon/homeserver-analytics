import asyncio
import json
import tempfile
import unittest
from pathlib import Path
from unittest.mock import AsyncMock
import sys
sys.path.insert(0, str(Path(__file__).parents[1] / 'integrations/serverbot/legacy'))
from aiohttp import web
from monitoring_webhooks import MonitoringWebhooks

class Request:
    path = '/grafana'
    content_type = 'application/json'
    headers = {'Authorization': 'Bearer ' + 'g' * 32}
    async def read(self):
        return json.dumps(self.payload).encode()
    payload = {'alerts': [{'labels': {'project': 'cottontoys', 'alertname': 'Down'},
                          'fingerprint': 'abc123', 'status': 'firing',
                          'startsAt': '2026-11-06T12:00:00Z'}]}

class WebhooksTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.bot = AsyncMock()
        self.bridge = MonitoringWebhooks(self.bot, 42, self.tmp.name, 'g'*32, 'k'*32,
                                         '172.30.88.1', capacity=1)
    async def asyncTearDown(self):
        await self.bridge.close()
        self.tmp.cleanup()
    async def test_durable_duplicate_and_restore(self):
        self.assertEqual((await self.bridge.receive(Request())).status, 202)
        await self.bridge.receive(Request())
        self.assertEqual(self.bridge.db.execute('select count(*) from events').fetchone()[0], 1)
        await self.bridge.deliver_once()
        await self.bridge.deliver_once()
        self.bot.send_message.assert_awaited_once()
        self.assertIn('cottontoys', self.bot.send_message.call_args.args[1])
        self.assertEqual(self.bridge.db.execute('select delivered from events').fetchone()[0], 1)
    async def test_failure_retained(self):
        await self.bridge.receive(Request())
        self.bot.send_message.side_effect = RuntimeError('offline')
        await self.bridge.deliver_once()
        self.assertEqual(self.bridge.db.execute('select delivered,attempts from events').fetchone(), (0,1))
    async def test_auth(self):
        req = Request(); req.headers = {}
        with self.assertRaises(web.HTTPUnauthorized): await self.bridge.receive(req)
    async def test_bounded(self):
        await self.bridge.receive(Request())
        req = Request(); req.payload = json.loads(json.dumps(Request.payload))
        req.payload['alerts'][0]['fingerprint'] = 'new'
        with self.assertRaises(web.HTTPServiceUnavailable): await self.bridge.receive(req)
    async def test_datasource_failure_is_infrastructure(self):
        event = json.loads(json.dumps(Request.payload))
        event['alerts'][0]['labels']={'alertname':'DatasourceError'}
        self.assertIn('infrastructure', self.bridge.normalize('grafana',event)[0][1])
        event['alerts'][0]['labels']={'alertname':'ArbitraryAlert'}
        with self.assertRaises(ValueError): self.bridge.normalize('grafana',event)

    async def test_recovery_and_kuma(self):
        event = json.loads(json.dumps(Request.payload)); event['alerts'][0]['status']='resolved'
        self.assertIn('Восстановление', self.bridge.normalize('grafana', event)[0][1])
        event = {'project':'talk','service':'readiness','heartbeat':{'status':0,'time':'2026-11-06 12:00:00'}}
        self.assertIn('Проблема', self.bridge.normalize('kuma', event)[0][1])
        event['heartbeat']['status']=2
        with self.assertRaises(ValueError): self.bridge.normalize('kuma', event)
    async def test_http_validation_and_size_limit(self):
        from aiohttp.test_utils import TestClient, TestServer
        async with TestClient(TestServer(self.bridge.app)) as client:
            headers={'Authorization':'Bearer '+'g'*32,'Content-Type':'application/json'}
            response=await client.post('/grafana',data='{' ,headers=headers)
            self.assertEqual(response.status,400)
            response=await client.post('/grafana',data='x'*65537,headers=headers)
            self.assertEqual(response.status,413)
            response=await client.post('/grafana',json=Request.payload)
            self.assertEqual(response.status,401)
            response=await client.post('/grafana',json=Request.payload,headers=headers)
            self.assertEqual(response.status,202)
            self.assertEqual(self.bridge.db.execute('select count(*) from events').fetchone()[0],1)

    async def test_reopen_inbox(self):
        await self.bridge.receive(Request())
        await self.bridge.close()
        self.bridge = MonitoringWebhooks(self.bot,42,self.tmp.name,'g'*32,'k'*32,'172.30.88.1')
        await self.bridge.deliver_once()
        self.bot.send_message.assert_awaited_once()

if __name__ == '__main__': unittest.main()
