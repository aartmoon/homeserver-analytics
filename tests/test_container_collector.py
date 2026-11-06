import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

path=Path(__file__).parents[1]/'scripts/container-collector.py'
spec=importlib.util.spec_from_file_location('collector',path)
collector=importlib.util.module_from_spec(spec);spec.loader.exec_module(collector)

class CollectorTest(unittest.TestCase):
    def test_counter_dedup_and_restart_delta(self):
        with tempfile.TemporaryDirectory() as temp:
            root=Path(temp);(root/'textfile').mkdir()
            count=[3]
            def docker(*args):
                if args[0]=='ps':return 'abc'
                if args[0]=='inspect':return json.dumps([{'Id':'abc','Name':'/ruk-app-1','RestartCount':count[0],'State':{'Running':True},'HostConfig':{'RestartPolicy':{'Name':'unless-stopped'}},'Config':{'Labels':{'com.docker.compose.project':'ruk','com.docker.compose.service':'app'}}}])
                if args[0]=='events':return json.dumps({'timeNano':123,'id':'abc','Actor':{'Attributes':{'name':'ruk-app-1'}}})
                if args[0]=='stats':return json.dumps({'Name':'ruk-app-1','MemUsage':'12MiB / 1GiB','CPUPerc':'2.5%'})
                raise AssertionError(args)
            with patch.object(collector,'ROOT',root),patch.object(collector,'STATE',root/'state'),patch.object(collector,'docker',docker):
                collector.main();count[0]=4;collector.main()
            state=json.loads((root/'state/state.json').read_text())['containers']['ruk-app-1']
            self.assertEqual(state['restarts'],4)
            self.assertEqual(state['oom'],1)
            metrics=(root/'textfile/containers.prom').read_text()
            self.assertIn('12582912.0',metrics)
            self.assertIn('0.025',metrics)
            self.assertIn('project="cottontoys"',metrics)
    def test_shared_dependencies(self):
        self.assertEqual(collector.project_of('ruk','postgres'),'infrastructure')
        self.assertEqual(collector.project_of('talk','backend'),'talk')

if __name__=='__main__':unittest.main()
