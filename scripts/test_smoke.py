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


    def test_wait_is_one_declarative_service_request(self):
        args = {'device': 'one', 'lease': 'lease'}
        conditions = [smoke.condition(role='textfield', value='Mars'), smoke.condition(role='text', label='Mars')]
        stats = {'observations': 3, 'observationErrors': 0}
        observation = {'device': 'one', 'elements': []}
        with patch.object(smoke, 'raw', return_value={'ok': True, 'result': {'observation': observation, 'attempts': 4}}) as raw:
            self.assertIs(smoke.wait_for(args, conditions, stats, mode='all'), observation)
        raw.assert_called_once_with('wait', dict(args, conditions=conditions, mode='all', timeoutMs=30000))
        self.assertEqual(stats['observations'], 3)
        self.assertEqual(stats['waitAttempts'], 4)
        self.assertEqual(stats['waits'], 1)

    def test_wait_failure_is_terminal_and_keeps_partial_diagnostics(self):
        partial = {'lastObservation': {'id': 'diagnostic'}, 'attempts': 2, 'satisfied': [0]}
        stats = {'observations': 0, 'observationErrors': 0}
        with patch.object(smoke, 'raw', return_value={'ok': False, 'result': partial,
                'error': {'code': 'wait_timeout'}}) as raw:
            with self.assertRaisesRegex(RuntimeError, 'wait_timeout'):
                smoke.wait_for({'device': 'one', 'lease': 'lease'}, [smoke.condition(label='Mars')], stats)
        self.assertEqual(raw.call_count, 1)
        self.assertEqual(stats['lastWaitFailure'], partial)
        self.assertEqual(stats['waitErrors'], 1)
        self.assertEqual(stats['observations'], 0)
        self.assertEqual(stats['observationErrors'], 0)

    def test_exercise_waits_for_onboarding_any_and_query_result_all(self):
        query = ''
        sequence = 0
        requests = []
        with tempfile.TemporaryDirectory() as directory:
            screenshot = Path(directory) / 'screen.jpg'
            screenshot.write_bytes(b'image')
            def observation():
                nonlocal sequence
                sequence += 1
                return {'id': str(sequence), 'device': '0', 'durationMs': 1, 'screenshot': str(screenshot),
                    'elements': [{'ref': 'field', 'role': 'textfield', 'label': '', 'value': query,
                        'actionable': True, 'actions': ['type']},
                        {'ref': 'result', 'role': 'text', 'label': query, 'actionable': False}]}
            def raw(method, args):
                if method == 'acquire': return {'ok': False, 'error': {'code': 'device_busy'}}
                if method == 'act': return {'ok': False, 'error': {'code': 'stale_observation'}}
                self.assertEqual(method, 'wait')
                requests.append(args)
                return {'ok': True, 'result': {'observation': observation(), 'attempts': 2}}
            def call(method, args):
                if method == 'acquire': return {'token': 'lease'}
                return {}
            def act(args, obs, element, action, **kw):
                nonlocal query
                self.assertEqual(action, 'type')
                query = kw['text']
                return observation()
            def observe(args, stats):
                stats['observations'] += 1
                return observation(), 1
            with patch.object(smoke, 'raw', side_effect=raw), patch.object(smoke, 'call', side_effect=call), \
                    patch.object(smoke, 'act', side_effect=act), patch.object(smoke, 'observe', side_effect=observe):
                result = smoke.exercise(self.fleet['phones'][0], 'Saturn', ['Saturn'], threading.Barrier(1), 1)
        self.assertEqual(result['status'], 'passed', result)
        self.assertEqual(requests[0]['mode'], 'any')
        self.assertTrue(all(c['evidence'] == 'native-hit' and c['where']['action'] in ('press', 'type')
                            for c in requests[0]['conditions']))
        self.assertEqual(requests[-1]['mode'], 'all')
        self.assertEqual(requests[-1]['conditions'], [smoke.condition(role='textfield', value='Saturn'),
                                                    smoke.condition(role='text', label='Saturn')])
        self.assertEqual(result['observations'], 5)
        self.assertEqual(result['waitAttempts'], 6)


class VisionWaitChecks(unittest.TestCase):
    def test_unacknowledged_input_observes_once_without_replaying(self):
        spec = importlib.util.spec_from_file_location('verify_live_vision', Path(__file__).with_name('verify-live-vision.py'))
        vision = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(vision)
        args = {'device': 'one', 'lease': 'lease'}
        obs = {'id': 'old', 'elements': [{'role': 'textfield', 'label': '', 'actionable': True, 'actions': ['type']}]}
        fresh = {'id': 'fresh', 'device': 'one', 'elements': []}
        checks = {}
        with patch.object(vision.SMOKE, 'act', side_effect=RuntimeError('Text replacement not acknowledged; inspect before retrying')) as act, \
                patch.object(vision.SMOKE, 'raw', return_value={'ok': True, 'result': {'observation': fresh, 'attempts': 1}}) as raw:
            self.assertIs(vision.type_and_observe(args, obs, 'Mars', checks), fresh)
        self.assertEqual(act.call_count, 1)
        raw.assert_called_once_with('wait', dict(args, conditions=[smoke.condition(role='textfield', value='Mars')], mode='all', timeoutMs=30000))
        self.assertEqual(checks['inputsConfirmedByFreshObservation'], ['Mars'])


if __name__=='__main__': unittest.main()
