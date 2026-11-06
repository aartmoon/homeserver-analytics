import importlib.util
from pathlib import Path
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock,patch

ROOT=Path(__file__).parents[1]/'integrations/serverbot/legacy'
sys.path.insert(0,str(ROOT))

class HandlersTest(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.tmp=tempfile.TemporaryDirectory()
        config=SimpleNamespace(Config=lambda:SimpleNamespace(owner=42,token='test',proxy=None,data=Path(self.tmp.name)))
        spec=importlib.util.spec_from_file_location('test_bot_source',ROOT/'bot.py')
        self.module=importlib.util.module_from_spec(spec)
        with patch.dict(sys.modules,{'config':config}):spec.loader.exec_module(self.module)
        self.bot=AsyncMock()
    async def asyncTearDown(self):self.tmp.cleanup()
    async def test_owner_private_status(self):
        msg=SimpleNamespace(from_user=SimpleNamespace(id=42),chat=SimpleNamespace(type='private'))
        await self.module.handle_message(msg,self.bot)
        self.bot.send_message.assert_awaited_once()
        self.assertEqual(self.bot.send_message.call_args.args[0],42)
    async def test_foreign_and_group_messages_ignored(self):
        for owner,chat in [(43,'private'),(42,'group')]:
            await self.module.handle_message(SimpleNamespace(from_user=SimpleNamespace(id=owner),chat=SimpleNamespace(type=chat)),self.bot)
        self.bot.send_message.assert_not_awaited()
    async def test_button_access(self):
        callback=SimpleNamespace(from_user=SimpleNamespace(id=43),message=SimpleNamespace(chat=SimpleNamespace(type='private')),answer=AsyncMock())
        await self.module.handle_button(callback,self.bot)
        self.bot.send_message.assert_not_awaited();callback.answer.assert_awaited_once_with('Нет доступа',show_alert=True)
        callback.from_user.id=42
        await self.module.handle_button(callback,self.bot)
        self.bot.send_message.assert_awaited_once()
