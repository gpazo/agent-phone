#!/usr/bin/env python3
"""Verify independent, simultaneous Wikipedia searches through CLI and MCP."""
import argparse
import base64
import concurrent.futures
from datetime import datetime
import json
import select
from pathlib import Path
import statistics
import subprocess
import threading
import time

ROOT = Path(__file__).resolve().parent.parent
CLI = str(ROOT / 'build/agent-phone')


def raw(method, args):
    p = subprocess.run([CLI, 'call', method, json.dumps(args)], capture_output=True, text=True, timeout=180)
    if not p.stdout:
        raise RuntimeError(p.stderr.strip() or f'{method} produced no response')
    return json.loads(p.stdout)


def call(method, args):
    r = raw(method, args)
    if not r['ok']:
        raise RuntimeError(r['error'])
    return r['result']


def require(value, message):
    if not value:
        raise RuntimeError(message)


def target(obs, label=None, role=None, action=None):
    matches = [e for e in obs['elements'] if e['actionable'] and (label is None or e['label'] == label)
               and (role is None or e['role'] == role) and (action is None or action in e.get('actions', []))]
    return matches[0] if len(matches) == 1 else None


def has_query(obs, query):
    return any(e['role'] == 'textfield' and e.get('value') == query for e in obs['elements'])


def has_result(obs, query):
    return any(e['role'] == 'text' and e['label'] == query for e in obs['elements'])


def observe(args, stats):
    started = time.monotonic()
    try:
        o = call('observe', args)
        require(o['device'] == args['device'], 'Observation belongs to a different device')
        stats['observations'] += 1
        return o, round((time.monotonic()-started)*1000, 2)
    except Exception:
        stats['observationErrors'] += 1
        raise


def condition(evidence='tree', **where):
    return {'where': where, 'evidence': evidence}


def wait_for(args, conditions, stats, timeout=30, mode='all'):
    stats['waits'] = stats.get('waits', 0) + 1
    try:
        reply = raw('wait', dict(args, conditions=conditions, mode=mode, timeoutMs=int(timeout*1000)))
        result = reply.get('result') or {}
        stats['waitAttempts'] = stats.get('waitAttempts', 0) + result.get('attempts', 0)
        if not reply['ok']:
            stats['lastWaitFailure'] = result
            raise RuntimeError(reply['error'])
        obs = result['observation']
        require(obs['device'] == args['device'], 'Wait observation belongs to a different device')
        return obs
    except Exception:
        stats['waitErrors'] = stats.get('waitErrors', 0) + 1
        raise



def act(args, obs, element, action, **kw):
    require(element is not None, 'Expected one verified target')
    r = call('act', dict(args, observation=obs['id'], ref=element['ref'], action=action, **kw))
    if 'observation' not in r:
        raise RuntimeError(r.get('observationError', 'No post-action observation'))
    return r['observation']


def summarize(samples):
    return {'samplesMs': samples, 'medianMs': statistics.median(samples), 'minMs': min(samples), 'maxMs': max(samples)}


def exercise(phone, query, all_queries, barrier, sample_count):
    r = {'name': phone['name'], 'device': phone['udid'], 'query': query, 'status': 'failed',
         'observations': 0, 'observationErrors': 0, 'expectedRejections': 0}
    args = None
    try:
        lease = call('acquire', {'device': phone['udid'], 'owner': 'wikipedia-smoke:'+query})
        args = {'device': phone['udid'], 'lease': lease['token']}
        conflict = raw('acquire', {'device': phone['udid'], 'owner': 'competing-agent'})
        require(not conflict['ok'] and conflict['error']['code'] == 'device_busy', 'Competing client acquired this device')
        r['expectedRejections'] += 1
        call('launch', dict(args, bundleId='org.wikimedia.wikipedia'))
        obs = wait_for(args, [condition('native-hit', label='Skip', action='press'),
                              condition('native-hit', label='Search', action='press'),
                              condition('native-hit', role='textfield', action='type')], r, mode='any')
        skip = target(obs, 'Skip', action='press')
        if skip:
            act(args, obs, skip, 'press')
            obs = wait_for(args, [condition('native-hit', label='Search', action='press')], r)
        if not target(obs, role='textfield', action='type'):
            act(args, obs, target(obs, 'Search', action='press'), 'press')
            obs = wait_for(args, [condition('native-hit', role='textfield', action='type')], r)
        preflight = 'Agent phone check '+query
        obs = act(args, obs, target(obs, role='textfield', action='type'), 'type', text=preflight)
        obs = wait_for(args, [condition(role='textfield', value=preflight)], r)
        r['preflightValueObserved'] = preflight
        barrier.wait(timeout=90)
        old, _ = observe(args, r)
        obs, _ = observe(args, r)
        rejected = raw('act', dict(args, observation=old['id'], ref=target(old, role='textfield', action='type')['ref'], action='type', text='MUST NOT APPEAR'))
        require(not rejected['ok'] and rejected['error']['code'] == 'stale_observation', 'Superseded observation accepted')
        r['expectedRejections'] += 1
        r['searchStartedMonotonic'] = time.monotonic()
        act(args, obs, target(obs, role='textfield', action='type'), 'type', text=query)
        obs = wait_for(args, [condition(role='textfield', value=query), condition(role='text', label=query)], r, mode='all')
        r['searchFinishedMonotonic'] = time.monotonic()
        r['searchElapsedMs'] = round((r['searchFinishedMonotonic']-r['searchStartedMonotonic'])*1000,2)
        r['timings'] = {}
        measurements = {False: ([],[]), True: ([],[])}
        barrier.wait(timeout=90)
        for _ in range(sample_count):
            for screenshot in [False, True]:
                obs, elapsed = observe(dict(args, screenshot=screenshot), r)
                require(has_query(obs,query) and has_result(obs,query), 'Query/result changed during measured observation')
                if screenshot:
                    require(Path(obs['screenshot']).stat().st_size > 0, 'Missing screenshot bytes')
                measurements[screenshot][0].append(obs['durationMs'])
                measurements[screenshot][1].append(elapsed)
        for screenshot, (service, client) in measurements.items():
            r['timings']['withScreenshot' if screenshot else 'accessibilityOnly'] = {'service':summarize(service), 'client':summarize(client)}
        r['observation'] = obs
        r['readyForFinalCheckMonotonic'] = time.monotonic()
        barrier.wait(timeout=90)
        obs, _ = observe(dict(args,screenshot=True),r)
        require(has_query(obs,query) and has_result(obs,query), 'Final device state does not contain its own query/result')
        require(not any(has_query(obs,q) for q in all_queries if q != query), 'Another device query leaked into this phone')
        r['finalVerifiedMonotonic'] = time.monotonic()
        r['observation'] = obs
        r['status'] = 'passed'
    except Exception as error:
        r['error'] = str(error) or type(error).__name__
        barrier.abort()
    finally:
        if args:
            try:
                call('release',args)
                r['releasedMonotonic'] = time.monotonic()
            except Exception as error:
                r['status'] = 'failed'
                r['cleanupError'] = str(error)
    return r


def mcp_check(device):
    lease = call('acquire', {'device': device, 'owner': 'mcp-smoke'})
    args = {'device': device, 'lease': lease['token'], 'screenshot': True}
    requests = [
        {'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2025-06-18','capabilities':{},'clientInfo':{'name':'smoke','version':'1'}}},
        {'jsonrpc':'2.0','id':2,'method':'tools/list','params':{}},
        {'jsonrpc':'2.0','id':3,'method':'tools/call','params':{'name':'phone_observe','arguments':args}}
    ]
    try:
        process = subprocess.Popen([CLI,'mcp'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=True)
        responses={}
        try:
            for request in requests:
                process.stdin.write(json.dumps(request)+'\n')
                process.stdin.flush()
                require(select.select([process.stdout],[],[],45)[0], 'MCP response timed out')
                response=json.loads(process.stdout.readline())
                require(response.get('id')==request['id'] and response.get('jsonrpc')=='2.0', 'MCP response ID/version mismatch')
                responses[request['id']]=response['result']
                if request['method']=='initialize':
                    process.stdin.write(json.dumps({'jsonrpc':'2.0','method':'notifications/initialized'})+'\n')
                    process.stdin.flush()
        finally:
            process.stdin.close()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
            process.stdout.close()
        result=responses[3]
        require(not result['isError'], 'MCP observation failed')
        require({b['type'] for b in result['content']} == {'text','image'}, 'MCP text/image pair missing')
        text_blocks=[b for b in result['content'] if b['type']=='text']
        image_blocks=[b for b in result['content'] if b['type']=='image']
        require(len(text_blocks)==1 and len(image_blocks)==1, 'MCP content count mismatch')
        payload=json.loads(text_blocks[0]['text'])
        require(payload['ok'] and payload['result']['device']==device, 'MCP text belongs to another device')
        image=image_blocks[0]
        pixels=base64.b64decode(image['data'],validate=True)
        require(image['mimeType']=='image/jpeg' and pixels.startswith(b'\xff\xd8') and pixels.endswith(b'\xff\xd9'), 'MCP image is not a complete JPEG')
        require(pixels==Path(payload['result']['screenshot']).read_bytes(), 'MCP bytes differ from the reported screenshot')
        return {'status':'passed','toolCount':len(responses[2]['tools']),'observationAndImage':True}
    finally:
        call('release', args)


def select_phones(manifest, fleet, queries):
    variants=manifest['variants']
    require(1 <= len(variants) <= 4, 'Smoke run supports one to four variants')
    require(len(queries) == len(variants) and len(set(queries)) == len(queries), 'Supply exactly one distinct query per variant')
    pairs=[]
    for v,q in zip(variants,queries):
        name=manifest['name']+' / '+v['name']
        matches=[p for p in fleet['phones'] if p['managed'] and p['name']==name]
        require(len(matches)==1, 'Expected exactly one managed phone named '+name)
        require(matches[0]['state']=='Booted', name+' is not running')
        pairs.append((matches[0],q))
    require(len({p['udid'] for p,_ in pairs})==len(pairs), 'Variants share a simulator')
    return pairs


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument('--matrix',type=Path,default=ROOT/'examples/wikipedia.json')
    parser.add_argument('--queries',nargs='+',default=['Mars','Ocean'])
    parser.add_argument('--samples',type=int,default=5)
    parser.add_argument('--prepare',action='store_true')
    parser.add_argument('--minimum-budget-mib',type=int,default=0)
    parser.add_argument('--output',type=Path,default=ROOT/'.cache/wikipedia-smoke.json')
    options=parser.parse_args()
    require(1 <= options.samples <= 30, 'samples must be 1–30')
    report={'status':'failed','matrix':str(options.matrix.resolve()),'results':[]}
    start=time.monotonic()
    try:
        manifest=json.loads(options.matrix.read_text())
        report['initialFleet']=call('fleet',{})
        require(report['initialFleet']['budgetBytes'] >= options.minimum_budget_mib*1024*1024,
                f'The running service needs a {options.minimum_budget_mib} MiB budget for this demo. Restart agent-phone serve with AGENT_PHONE_BUDGET_MIB={options.minimum_budget_mib}; existing service settings are not changed by client environment variables.')
        if options.prepare:
            matrix_reply=raw('matrix',{'matrix':str(options.matrix.resolve())})
            report['matrixRun']=matrix_reply.get('result')
            require(matrix_reply['ok'], 'Matrix setup failed; inspect matrixRun in the report: '+str(matrix_reply.get('error')))
            require(all(v['status'] in ('observed','passed') for v in report['matrixRun']['results']), 'Matrix setup failed; inspect the per-device matrixRun results in the report')
            report['initialFleet']=call('fleet',{})
        pairs=select_phones(manifest,report['initialFleet'],options.queries)
        barrier=threading.Barrier(len(pairs))
        with concurrent.futures.ThreadPoolExecutor(max_workers=len(pairs)) as pool:
            futures=[pool.submit(exercise,p,q,options.queries,barrier,options.samples) for p,q in pairs]
            report['results']=[future.result() for future in futures]
        require(all(v['status']=='passed' for v in report['results']), 'One or more device checks failed')
        latest_start=max(v['searchStartedMonotonic'] for v in report['results'])
        earliest_end=min(v['searchFinishedMonotonic'] for v in report['results'])
        report['searchOverlapMs']=round((earliest_end-latest_start)*1000,2)
        require(earliest_end>latest_start, 'Search operation intervals did not overlap across every device')
        report['simultaneousVerifiedDevices']=len(pairs)
        report['mcp']=[dict(device=p['udid'],**mcp_check(p['udid'])) for p,_ in pairs]
        report['fleet']=call('fleet',{})
        select_phones(manifest,report['fleet'],options.queries)
        report['status']='passed'
    except Exception as error:
        report['error']=str(error) or type(error).__name__
    finally:
        report['completedAt']=datetime.now().astimezone().isoformat()
        report['elapsedSeconds']=round(time.monotonic()-start,2)
        report['observationErrors']=sum(v.get('observationErrors',0) for v in report['results'])
        options.output.parent.mkdir(parents=True,exist_ok=True)
        options.output.write_text(json.dumps(report,indent=2)+'\n')
    for r in report['results']:
        print(r['name'],r['status'],r['query'],r.get('timings',r.get('error')),flush=True)
    print('Status:',report['status'],'Evidence:',options.output,flush=True)
    if report['status']!='passed':
        raise SystemExit(report.get('error','Smoke failed'))


if __name__=='__main__':
    main()
