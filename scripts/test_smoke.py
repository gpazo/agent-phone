import importlib.util
from pathlib import Path
import threading
import tempfile
import json
import unittest
from unittest.mock import patch

spec=importlib.util.spec_from_file_location('smoke',Path(__file__).with_name('smoke-wikipedia.py'))
smoke=importlib.util.module_from_spec(spec)
spec.loader.exec_module(smoke)


class SmokeChecks(unittest.TestCase):
    def setUp(self):
        self.manifest={'name':'Wiki','variants':[{'name':str(i)} for i in range(4)]}
        self.queries=['Saturn','Volcano','Jupiter','Forest']
        self.fleet={'phones':[{'name':'Wiki / '+str(i),'managed':True,'udid':str(i),'state':'Booted'} for i in range(4)]}

    def test_four_distinct_running_phones_required(self):
        self.assertEqual(len(smoke.select_phones(self.manifest,self.fleet,self.queries)),4)
        self.fleet['phones'][3]['state']='Shutdown'
        with self.assertRaisesRegex(RuntimeError,'not running'):
            smoke.select_phones(self.manifest,self.fleet,self.queries)

    def test_duplicate_query_cannot_hide_routing_errors(self):
        with self.assertRaisesRegex(RuntimeError,'distinct query'):
            smoke.select_phones(self.manifest,self.fleet,['Mars']*4)

    def test_duplicate_named_devices_are_not_selected_arbitrarily(self):
        self.fleet['phones'].append(dict(self.fleet['phones'][0],udid='extra'))
        with self.assertRaisesRegex(RuntimeError,'exactly one'):
            smoke.select_phones(self.manifest,self.fleet,self.queries)

    def test_worker_failure_releases_lease_and_unblocks_peers(self):
        barrier=threading.Barrier(4)
        calls=[]
        def call(method,args):
            calls.append(method)
            if method=='acquire': return {'token':'lease'}
            if method=='launch': raise RuntimeError('launch failed')
            if method=='release': return {'released':True}
        with patch.object(smoke,'call',side_effect=call), patch.object(smoke,'raw',return_value={'ok':False,'error':{'code':'device_busy'}}):
            result=smoke.exercise(self.fleet['phones'][0],'Saturn',self.queries,barrier,1)
        self.assertEqual(result['status'],'failed')
        self.assertEqual(result['error'],'launch failed')
        self.assertIn('release',calls)
        self.assertTrue(barrier.broken)

    def test_failed_matrix_preserves_partial_report(self):
        matrix={'status':'failed','report':'/evidence/run.json','results':[{'name':'Light','status':'failed','error':'boot failed'}]}
        def raw(method,args):
            if method=='fleet':
                return {'ok':True,'result':{'budgetBytes':4096*1024*1024}}
            if method=='matrix':
                return {'ok':False,'result':matrix,'error':{'code':'matrix_failed','message':'One variant failed'}}
            self.fail('Unexpected operation '+method)
        with tempfile.TemporaryDirectory() as directory:
            manifest=Path(directory)/'matrix.json'
            output=Path(directory)/'report.json'
            manifest.write_text(json.dumps(self.manifest))
            args=['smoke-wikipedia.py','--prepare','--matrix',str(manifest),'--output',str(output)]
            with patch('sys.argv',args), patch.object(smoke,'raw',side_effect=raw):
                with self.assertRaises(SystemExit):
                    smoke.main()
            report=json.loads(output.read_text())
            self.assertEqual(report['status'],'failed')
            self.assertEqual(report.get('matrixRun'),matrix)


if __name__=='__main__': unittest.main()
