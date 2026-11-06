"""Authenticated monitoring webhooks with a durable, bounded inbox.
Only uses the supplied Bot; never creates a session or starts polling.
"""
import asyncio
import hashlib
import hmac
import ipaddress
import json
import os
import re
import sqlite3
import time
from pathlib import Path
from aiohttp import web

SAFE = re.compile(r'^[a-zA-Z0-9_.:-]{1,80}$')


class MonitoringWebhooks:
    def __init__(self, bot, owner_id, data, grafana_secret, kuma_secret,
                 host, port=9088, capacity=1000):
        address = ipaddress.ip_address(host)
        if address.is_unspecified or address.is_multicast or not (address.is_loopback or address.is_private):
            raise ValueError('Bind only to loopback or a dedicated private bridge address')
        if min(len(grafana_secret), len(kuma_secret)) < 32:
            raise ValueError('Webhook secrets must contain at least 32 characters')
        self.bot, self.owner_id = bot, owner_id
        self.secrets = {'grafana': grafana_secret, 'kuma': kuma_secret}
        self.host, self.port, self.capacity = host, port, capacity
        path = Path(data) / 'monitoring-inbox.sqlite3'
        fd = os.open(path, os.O_CREAT | os.O_RDWR, 0o600)
        os.close(fd)
        path.chmod(0o600)
        self.db = sqlite3.connect(path)
        self.db.execute('PRAGMA journal_mode=WAL')
        self.db.execute('PRAGMA synchronous=FULL')
        self.db.execute('CREATE TABLE IF NOT EXISTS events '
                        '(id TEXT PRIMARY KEY, text TEXT NOT NULL, delivered INTEGER DEFAULT 0, '
                        'created REAL NOT NULL, attempts INTEGER DEFAULT 0, retry REAL DEFAULT 0)')
        self.db.commit()
        self.app = web.Application(client_max_size=64 * 1024)
        self.app.router.add_post('/grafana', self.receive)
        self.app.router.add_post('/kuma', self.receive)
        self.runner = None
        self.worker = None
        self.wake = asyncio.Event()
        self.stopping = False

    @staticmethod
    def clean(value):
        if not isinstance(value, str) or not SAFE.fullmatch(value):
            raise ValueError('Invalid label')
        return value

    def normalize(self, source, payload):
        if not isinstance(payload, dict):
            raise ValueError('JSON object required')
        result = []
        if source == 'grafana':
            alerts = payload.get('alerts')
            if not isinstance(alerts, list) or not 1 <= len(alerts) <= 100:
                raise ValueError('Expected 1..100 alerts')
            for alert in alerts:
                labels = alert['labels']
                name = self.clean(labels['alertname'])
                project = self.clean(labels.get('project', 'infrastructure'
                    if name in {'DatasourceError', 'DatasourceNoData'} else ''))
                status = alert['status']
                if status not in {'firing', 'resolved'}:
                    raise ValueError('Invalid status')
                fingerprint = self.clean(alert['fingerprint'])
                start = alert['startsAt']
                end = alert.get('endsAt', '') if status == 'resolved' else ''
                if not isinstance(start, str) or not 1 <= len(start) <= 64 or not isinstance(end, str) or len(end) > 64:
                    raise ValueError('Invalid timestamp')
                identity = ['grafana', fingerprint, status, start, end]
                service = self.clean(labels.get('service', 'monitoring'))
                instance = self.clean(labels.get('instance', 'homeserver'))
                severity = self.clean(labels.get('severity', 'warning'))
                result.append((identity, project, name, status, service, instance, severity))
        else:
            # Custom Kuma webhook body; see README for exact template.
            project = self.clean(payload['project'])
            name = self.clean(payload['service'])
            heartbeat = payload['heartbeat']
            status_value = heartbeat['status']
            if type(status_value) is not int or status_value not in (0, 1):
                raise ValueError('Only DOWN/UP heartbeats accepted')
            stamp = heartbeat['time']
            if not isinstance(stamp, str) or not 1 <= len(stamp) <= 64:
                raise ValueError('Invalid heartbeat time')
            status = 'resolved' if status_value == 1 else 'firing'
            result.append((['kuma', project, name, status, stamp], project, name, status, name, name, 'warning'))
        return [(hashlib.sha256(json.dumps(identity).encode()).hexdigest(),
                 f"{'🔴 Проблема' if status == 'firing' else '🟢 Восстановление'}\n"
                 f'Проект: {project}\nСервис: {service}\nЭкземпляр: {instance}\nПроверка: {name}\nУровень: {severity}')
                for identity, project, name, status, service, instance, severity in result]

    async def receive(self, request):
        source = request.path.removeprefix('/')
        supplied = request.headers.get('Authorization', '')
        expected = 'Bearer ' + self.secrets[source]
        if not hmac.compare_digest(supplied.encode(), expected.encode()):
            raise web.HTTPUnauthorized()
        if request.content_type != 'application/json':
            raise web.HTTPUnsupportedMediaType()
        try:
            async with asyncio.timeout(5):
                body = await request.read()
            events = self.normalize(source, json.loads(body))
        except (ValueError, KeyError, TypeError, AttributeError, asyncio.TimeoutError):
            raise web.HTTPBadRequest(text='Invalid monitoring event') from None
        try:
            with self.db:
                self.db.execute('DELETE FROM events WHERE delivered=1 AND created < ?',
                                (time.time() - 7 * 86400,))
                new = {key: text for key, text in events if not self.db.execute(
                    'SELECT 1 FROM events WHERE id=?', (key,)).fetchone()}
                pending = self.db.execute('SELECT count(*) FROM events WHERE delivered=0').fetchone()[0]
                total = self.db.execute('SELECT count(*) FROM events').fetchone()[0]
                if pending + len(new) > self.capacity or total + len(new) > 10000:
                    raise web.HTTPServiceUnavailable(headers={'Retry-After': '60'})
                self.db.executemany('INSERT INTO events(id,text,created) VALUES (?,?,?)',
                                    [(key, text, time.time()) for key, text in new.items()])
        except sqlite3.Error:
            raise web.HTTPServiceUnavailable(text='Inbox unavailable', headers={'Retry-After': '60'}) from None
        self.wake.set()
        return web.json_response({'accepted': True}, status=202)

    async def deliver_once(self):
        row = self.db.execute('SELECT id,text,attempts FROM events '
                              'WHERE delivered=0 AND retry <= ? ORDER BY created LIMIT 1',
                              (time.time(),)).fetchone()
        if not row:
            return False
        key, text, attempts = row
        try:
            async with asyncio.timeout(30):
                await self.bot.send_message(self.owner_id, text, parse_mode=None)
        except asyncio.CancelledError:
            raise
        except Exception:
            # Do not log exception text: SDK errors may contain credentials.
            with self.db:
                self.db.execute('UPDATE events SET attempts=attempts+1,retry=? WHERE id=?',
                                (time.time() + min(3600, 2 ** min(attempts + 1, 12)), key))
        else:
            with self.db:
                self.db.execute('UPDATE events SET delivered=1 WHERE id=?', (key,))
        return True

    async def run(self):
        while not self.stopping:
            try:
                delivered = await self.deliver_once()
            except sqlite3.Error:
                # A temporary storage failure must not silently kill the worker.
                await asyncio.sleep(5)
                continue
            if delivered:
                await asyncio.sleep(1)
                continue
            self.wake.clear()
            try:
                await asyncio.wait_for(self.wake.wait(), timeout=2)
            except asyncio.TimeoutError:
                pass

    async def start(self):
        self.runner = web.AppRunner(self.app, access_log=None)
        await self.runner.setup()
        try:
            await web.TCPSite(self.runner, self.host, self.port).start()
        except BaseException:
            await self.runner.cleanup()
            raise
        self.worker = asyncio.create_task(self.run(), name='monitoring-inbox')

    async def close(self):
        if self.runner:
            await self.runner.cleanup()
        self.stopping = True
        self.wake.set()
        if self.worker:
            try:
                await asyncio.wait_for(asyncio.shield(self.worker), 5)
            except asyncio.TimeoutError:
                self.worker.cancel()
                await asyncio.gather(self.worker, return_exceptions=True)
        self.db.close()
