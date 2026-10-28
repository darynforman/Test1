#!/usr/bin/env python3
"""External, read-only browser recorder. The user starts every image job."""
import argparse
import csv
from datetime import datetime, timezone
import json
import getpass
import hashlib
import os
import signal
from pathlib import Path
import re
import shlex
import shutil
import statistics
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from urllib.parse import urlparse

HERE = Path(__file__).resolve().parent
PROJECT = HERE.parent
PREDICTIONS = {
    'A': 'Quiet processing: long polling should use fewer unchanged observation requests than short polling.',
    'B': 'Real processing: long polling may detect completion sooner, but closely spaced milestones can increase requests.',
    'C1': 'Two-second simulated updates: long polling should avoid many unchanged short-poll responses.',
    'C2': '200ms simulated updates: long polling may observe more versions and start more requests than short polling.',
    'C3': 'Fast updates with added latency: receipt delay and skipped intermediate versions may increase.',
}
COLUMNS = ['run_id','condition','mode','repetition','job_id','image','latency_setting','baseline_version','terminal_version','requests_started','initial_200','newer_200','unchanged_200','no_change_204','retrieval_errors','cancellations','distinct_new_versions_seen','processing_ms','detection_ms','notes','observation_db_reads']


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n')


def server_records(path, job_id):
    all_lines = path.read_text().splitlines() if path.exists() else []
    selected = [line for line in all_lines if f'job_id={job_id} ' in line]
    fields = [dict(part.split('=', 1) for part in shlex.split(line) if '=' in part) for line in selected]
    config = next((line for line in reversed(all_lines) if 'database connection pool established' in line), '')
    return selected, fields, config


def summarize(job, entries, fields, run_id, condition, repetition, image, latency, foreground):
    received = [e for e in entries if 'version' in e]
    baseline = received[0]['version'] if received else None
    versions = sorted({e['version'] for e in received})
    modes = sorted({e['mode'] for e in entries})
    mode = modes[0] if len(modes) == 1 else 'mixed'
    count = lambda outcome: sum(e['outcome'] == outcome for e in entries)
    terminal = next((e for e in reversed(received) if e['version'] == job['version']), None)
    commit = next((f for f in fields if f.get('msg') == 'job_commit' and f.get('status') in ('completed', 'failed') and int(f['version']) == job['version']), None)
    sql_reads = [int(f['db_reads']) for f in fields if f.get('msg') == 'observation']
    processing = None
    if job.get('started_at') and (job.get('completed_at') or job.get('failed_at')):
        parse = lambda v: datetime.fromisoformat(v.replace('Z', '+00:00'))
        processing = round((parse(job.get('completed_at') or job['failed_at']) - parse(job['started_at'])).total_seconds() * 1000, 3)
    committed = sorted({int(f['version']) for f in fields if f.get('msg') == 'job_commit' and baseline is not None and baseline < int(f['version']) <= job['version']})
    notes = ['Browser Date.now() at JSON receipt minus server post-commit commit_ms; millisecond resolution with scheduling uncertainty.', 'Short interval: 1000ms after each response. render_ms records DOM updates, not paint.']
    if not commit:
        notes.append('INCOMPLETE: matching post-commit terminal server log not found.')
    if not sql_reads:
        notes.append('INCOMPLETE: no matching observation database-read logs found.')
    if not foreground:
        notes.append('INVALID COMPARISON: browser tab was hidden during observation.')
    gaps=[entries[i]['start_ms']-entries[i-1]['receipt_ms'] for i in range(1,len(entries)) if entries[i-1].get('receipt_ms') is not None]
    if mode == 'long' and any(gap>=250 for gap in gaps):
        notes.append('REVIEW: long-poll client gap >=250ms; cause not established. Check tab visibility and scheduling before counting this run.')
    if mode == 'mixed':
        notes.append('INVALID COMPARISON: observation mode changed during this job.')
    if any(e['outcome'] in ('pending', 'retrieval_error', 'timeout', 'cancelled') for e in entries):
        notes.append('Run includes pending requests, retrieval errors, timeouts, or cancellations; inspect raw log before comparison.')
    row = dict(run_id=run_id, condition=condition, mode=mode, repetition=repetition, job_id=job['id'], image=image, latency_setting=latency, baseline_version=baseline, terminal_version=job['version'], requests_started=len(entries), initial_200=count('initial_200'), newer_200=count('newer_200'), unchanged_200=count('unchanged_200'), no_change_204=count('no_change_204'), retrieval_errors=count('retrieval_error')+count('timeout'), cancellations=count('cancelled'), distinct_new_versions_seen=len([v for v in versions if baseline is not None and v > baseline]), processing_ms=processing, detection_ms=(terminal['receipt_ms']-int(commit['commit_ms'])) if terminal and commit else None, notes=' '.join(notes), observation_db_reads=sum(sql_reads) if sql_reads else None)
    details = dict(row, received_versions=versions, committed_after_baseline=committed, missing_versions=sorted(set(committed)-set(versions)), terminal_commit_ms=int(commit['commit_ms']) if commit else None, terminal_receipt_ms=terminal['receipt_ms'] if terminal else None, between_request_gaps_ms=gaps)
    return row, details


class Browser:
    def __init__(self, driver, network_tools=False):
        # Snap Firefox can access its own cache; keep temporary data outside the project.
        cache = Path.home() / 'snap/firefox/common/.cache'
        self.runtime = tempfile.TemporaryDirectory(
            prefix='imagelab-recorder-', dir=cache if cache.is_dir() else None)
        profiles = Path(self.runtime.name)
        log = profiles / 'driver.log'
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        self.url = f'http://127.0.0.1:{port}'
        self.session = None
        # WebDriver is local: never route its commands through environment proxies.
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.log_handle = log.open('w')
        self.process = None
        try:
            self.process = subprocess.Popen([driver, '--host', '127.0.0.1', '--port', str(port), '--profile-root', str(profiles)] + (['--allow-system-access'] if network_tools else []), stdout=self.log_handle, stderr=subprocess.STDOUT)
            for _ in range(150):
                if self.process.poll() is not None:
                    raise RuntimeError('Firefox driver stopped: ' + log.read_text()[-1500:])
                try:
                    self.call('GET', '/status', timeout=2)
                    break
                except (OSError, urllib.error.URLError, RuntimeError):
                    time.sleep(.2)
            else:
                raise RuntimeError('Firefox driver did not start: ' + log.read_text()[-1500:])
            opened = self.call('POST', '/session', {'capabilities': {'alwaysMatch': {'browserName': 'firefox', 'pageLoadStrategy': 'eager'}}}, timeout=60)
            self.session = opened['sessionId']
            self.capabilities = opened.get('capabilities', {})
            self.command('/timeouts', {'script':5000, 'pageLoad':20000, 'implicit':0})
        except BaseException:
            self.close()
            raise

    def call(self, method, endpoint, data=None, timeout=10):
        payload = json.dumps(data).encode() if data is not None else None
        req = urllib.request.Request(self.url + endpoint, data=payload, method=method, headers={'Content-Type': 'application/json'})
        try:
            with self.http.open(req, timeout=timeout) as response:
                result = json.load(response)['value']
        except urllib.error.HTTPError as exc:
            raise RuntimeError(f'{method} {endpoint}: '+exc.read().decode()) from exc
        except (TimeoutError, urllib.error.URLError) as exc:
            raise RuntimeError(f'{method} {endpoint}: browser connection failed ({exc}). No run has been declared successful.') from exc
        return result

    def command(self, endpoint, data=None, method='POST', timeout=10):
        return self.call(method, '/session/' + self.session + endpoint, data, timeout=timeout)

    def set_network_latency(self, milliseconds):
        """Use Firefox Network Monitor's own throttling actor, in this test profile only."""
        self.command('/timeouts', {'script':30000})
        self.command('/moz/context', {'context':'chrome'})
        try:
            value=self.command('/execute/async', {'script': """
                const latency=arguments[0], done=arguments[arguments.length-1];
                (async()=>{
                    const {require}=ChromeUtils.importESModule('resource://devtools/shared/loader/Loader.sys.mjs');
                    const {gDevTools}=require('devtools/client/framework/devtools');
                    const win=Services.wm.getMostRecentWindow('navigator:browser');
                    const toolbox=await gDevTools.showToolboxForTab(win.gBrowser.selectedTab,{toolId:'netmonitor'});
                    const front=await toolbox.commands.watcherFront.getNetworkParentActor();
                    await front.setNetworkThrottling(latency===null?null:{latency,downloadThroughput:1000000000,uploadThroughput:1000000000});
                    return await front.getNetworkThrottling();
                })().then(done,error=>done({error:String(error)}));
            """,'args':[milliseconds]},timeout=35)
            if isinstance(value,dict) and value.get('error'):
                raise RuntimeError('Firefox network tooling failed: '+value['error'])
            if milliseconds is not None and (not isinstance(value,dict) or value.get('latency')!=milliseconds):
                raise RuntimeError('Firefox did not confirm the requested latency: '+str(value))
            return value
        finally:
            self.command('/moz/context', {'context':'content'})
            self.command('/timeouts', {'script':5000})

    def close(self):
        if self.session:
            try:
                self.call('DELETE', '/session/' + self.session, timeout=3)
            except Exception:
                pass
            self.session = None
        if self.process is not None and self.process.poll() is None:
            self.process.terminate()
            try:
                self.process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
        self.log_handle.close()
        self.runtime.cleanup()


def repetition_for(condition, mode, results):
    repetitions = []
    for folder in (PROJECT / 'assessment2-results', results):
        if folder.exists():
            for file in folder.glob(f'{condition}-{mode}-*-summary.json'):
                match = re.fullmatch(rf'{re.escape(condition)}-{re.escape(mode)}-(\d+)-summary.json', file.name)
                if match:
                    repetitions.append(int(match.group(1)))
    return max(repetitions, default=0) + 1



def guided_pair(args):
    """Exactly two real UI uploads, then stop for review. No batch is started."""
    if args.repetition < 1:
        raise RuntimeError('Repetition must be a positive integer.')
    condition=args.condition
    settings={'A':('30s','0s'),'B':('0s','0s'),'C1':('0s','2s'),'C2':('0s','200ms'),'C3':('0s','200ms')}
    if condition not in settings:
        raise RuntimeError('This condition needs a verified latency setup before recording.')
    delay,interval=settings[condition]
    latency_setting='Firefox Network Monitor: latency=200ms; upload/download limit=1000000000 bytes/s; actual timings recorded per run' if condition=='C3' else 'No added latency'
    image = args.image.resolve()
    if not image.is_file():
        raise RuntimeError('Input image not found: ' + str(image))
    address = urlparse(args.url)
    if address.hostname not in ('localhost', '127.0.0.1'):
        raise RuntimeError('The guided pair requires the browser and server on this computer.')
    port = address.port or 4000
    connection = os.environ.get('IMAGELAB_DB_DSN') or f'host=/var/run/postgresql dbname=imagelab user={getpass.getuser()} sslmode=disable'
    def queue_empty():
        result = subprocess.run(['psql', connection, '-X', '-qAt', '-v', 'ON_ERROR_STOP=1', '-c',
                                 "SELECT count(*) FROM jobs WHERE status IN ('queued','processing');"],
                                text=True, capture_output=True, timeout=10)
        if result.returncode != 0:
            raise RuntimeError('Cannot check the worker queue: ' + result.stderr.strip())
        if int(result.stdout.strip()) != 0:
            raise RuntimeError('Worker queue is not empty. No new upload was started.')
    queue_empty()
    http = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        http.open(args.url + '/v1/healthcheck', timeout=2).close()
    except urllib.error.URLError:
        pass
    else:
        raise RuntimeError('An app is already running on this port. Leave it alone; stop and review its settings first.')
    results = args.results_dir.resolve() if args.results_dir else HERE/'results'/('restart-'+datetime.now().strftime('%Y%m%d-%H%M%S'))
    results.mkdir(parents=True, exist_ok=True)
    modes = (args.guided_mode,) if args.guided_mode else (('short','long') if args.repetition % 2 else ('long','short'))
    for mode in modes:
        if any(results.glob(condition+'-'+mode+'-'+str(args.repetition)+'-*')):
            raise RuntimeError('This run already has saved files; refusing to overwrite it.')
    if args.prediction_file:
        prediction_data=json.loads(args.prediction_file.read_text())
        if prediction_data.get('condition')!=condition:
            raise RuntimeError('The saved prediction does not match this condition.')
        prediction=prediction_data['prediction']
        prediction_author=prediction_data.get('author','user')
    else:
        raise RuntimeError('A user prediction must be saved before guided measurements. Supply --prediction-file.')
    label = (args.guided_mode or 'pair')+'-'+str(args.repetition)
    log = results/(condition+'-'+label+'-full-server.log')
    write_json(results/('configuration-'+condition+'-'+label+'.json'), {
        'condition':condition, 'prediction':prediction, 'prediction_author':prediction_author, 'prediction_reason':prediction_data.get('reason',''), 'written_before_runs':datetime.now(timezone.utc).isoformat(),
        'image':str(image), 'image_sha256':hashlib.sha256(image.read_bytes()).hexdigest(),
        'worker_delay':delay,'simulation_interval':interval,'latency_setting':latency_setting,
        'planned_runs':[condition+'-'+mode+'-'+str(args.repetition) for mode in modes],
        'method':'Real Firefox UI uploads to the unchanged Go application. Each job finishes before the next upload. Browser Date.now() receipt timestamps are joined with server post-commit commit_ms. Millisecond resolution and scheduling uncertainty apply.'})
    server = browser = None
    output = log.open('w')
    try:
        environment = dict(os.environ, IMAGELAB_DB_DSN=connection)
        server = subprocess.Popen(['make','--no-print-directory','run',f'PORT={port}',f'WORKER_DELAY={delay}',f'SIMULATION_INTERVAL={interval}'],
                                  cwd=PROJECT, env=environment, stdout=output, stderr=subprocess.STDOUT, start_new_session=True)
        for _ in range(300):
            if server.poll() is not None:
                raise RuntimeError('App did not start. See ' + str(log))
            try:
                http.open(args.url+'/v1/healthcheck',timeout=1).close()
                break
            except urllib.error.URLError:
                time.sleep(.2)
        else:
            raise RuntimeError('App startup timed out. See '+str(log))
        print('App ready. Opening Firefox for '+', '.join(modes)+' only.',flush=True)
        browser = Browser(args.driver, network_tools=condition=='C3')
        write_json(results/('browser-'+condition+'-'+label+'.json'),browser.capabilities)
        for mode in modes:
            queue_empty()
            browser.command('/url',{'url':args.url})
            access=browser.command('/execute/sync',{'script':"return {driver_scope:typeof activeJob,page_scope:window.eval(\"typeof activeJob\")};",'args':[]})
            if access['page_scope']!='object':
                raise RuntimeError('Recorder cannot read page job state; stopped before uploading: '+str(access))
            print('Page job-state access checked before upload: '+str(access),flush=True)
            def prompt_gets():
                checks=browser.command('/execute/async',{'script':"const done=arguments[arguments.length-1]; (async()=>{const readings=[];for(let i=0;i<5;i++){const start=performance.now();const r=await fetch('/v1/healthcheck',{cache:'no-store'});await r.text();readings.push({status:r.status,round_trip_ms:performance.now()-start});}done(readings);})().catch(e=>done({error:String(e)}));",'args':[]})
                if not isinstance(checks,list) or any(r['status']!=200 for r in checks):
                    raise RuntimeError('Prompt GET timing check failed: '+str(checks))
                return checks
            baseline_checks=None
            confirmed=None
            if condition=='C3':
                browser.set_network_latency(None)
                baseline_checks=prompt_gets()
                confirmed=browser.set_network_latency(200)
            prompt_checks=prompt_gets()
            timing={'latency_setting':latency_setting,'tool':'Firefox Network Monitor native throttling for C3; browser performance.now() for elapsed request timing only','configured':confirmed,'baseline_readings':baseline_checks,'readings':prompt_checks,'documentation':'https://firefox-source-docs.mozilla.org/devtools-user/network_monitor/throttling/index.html'}
            if condition=='C3':
                before=statistics.median(x['round_trip_ms'] for x in baseline_checks)
                after=statistics.median(x['round_trip_ms'] for x in prompt_checks)
                timing.update(baseline_median_ms=before,throttled_median_ms=after,measured_additional_median_ms=after-before)
                timing['latency_check_accepted']=150<=after-before<=350 and 180<=after<=500
                print(f'Latency check BEFORE upload: baseline median {before:.1f}ms; throttled median {after:.1f}ms; difference {after-before:.1f}ms.',flush=True)
            write_json(results/(condition+'-'+mode+'-'+str(args.repetition)+'-prompt-get.json'),timing)
            if condition=='C3' and not timing['latency_check_accepted']:
                raise RuntimeError('Measured latency is outside the verification range. No image uploaded for this run.')
            selected = browser.command('/execute/sync',{'script':"const s=document.querySelector('#observationMode'); s.value=arguments[0]; s.dispatchEvent(new Event('change',{bubbles:true})); return s.value;",'args':[mode]})
            if selected != mode:
                raise RuntimeError('Could not select the requested observation mode.')
            element = browser.command('/element',{'using':'css selector','value':'#file'})
            element_id = element['element-6066-11e4-a52e-4f735466cecf']
            browser.command('/element/'+element_id+'/value',{'text':str(image)})
            selected_image = browser.command('/execute/sync',{'script':"return document.querySelector('#file').files[0]?.name;",'args':[]})
            if selected_image != image.name:
                raise RuntimeError('The selected image does not match the configured input.')
            button = browser.command('/element',{'using':'css selector','value':'#process'})
            print('Starting '+condition+'-'+mode+'-'+str(args.repetition)+' through Process image. Keep the Firefox tab visible.',flush=True)
            browser.command('/element/'+button['element-6066-11e4-a52e-4f735466cecf']+'/click',{})
            deadline=time.monotonic()+90
            foreground=True
            snapshot=None
            while time.monotonic()<deadline:
                snapshot=browser.command('/execute/sync',{'script':"return {job:window.eval(\"typeof activeJob==='undefined'?null:activeJob\"),entries:window.imageLabObservationLog||[],visible:document.visibilityState==='visible',error:!document.querySelector('#retrievalError').hidden,uploadError:document.querySelector('#error').textContent};",'args':[]})
                foreground=foreground and snapshot['visible']
                job=snapshot['job']
                if snapshot['error'] or snapshot['uploadError']:
                    raise RuntimeError('The app reported an upload or retrieval error: '+str(snapshot))
                if job and job['status'] in ('completed','failed'):
                    entries=[e for e in snapshot['entries'] if e['job_id']==job['id']]
                    if entries and not any(e['outcome']=='pending' for e in entries):
                        break
                time.sleep(.25)
            else:
                if snapshot:write_json(results/(condition+'-'+mode+'-'+str(args.repetition)+'-incomplete.json'),snapshot)
                raise RuntimeError('The run did not finish within 90 seconds; no successful result claimed.')
            run_id=condition+'-'+mode+'-'+str(args.repetition)
            lines,fields,config=server_records(log,job['id'])
            row,details=summarize(job,entries,fields,run_id,condition,args.repetition,image.name,latency_setting,foreground)
            details['latency_timing']=timing
            details['validation']={'completed':job['status']=='completed','three_variants':job.get('variants_completed')==3 and len(job.get('variants',[]))==3,
                'foreground_samples_visible':foreground,'all_requests_accounted':len(entries)==sum(row[k] for k in ('initial_200','newer_200','unchanged_200','no_change_204','retrieval_errors','cancellations')),
                'one_mode':all(e['mode']==mode for e in entries),'one_initial_snapshot':row['initial_200']==1,
                'terminal_commit_found':details['terminal_commit_ms'] is not None,
                'server_settings_match':f'worker_delay={delay}' in config and f'simulation_interval={interval}' in config,
                'no_retrieval_errors_or_cancellations':row['retrieval_errors']==0 and row['cancellations']==0,
                'latency_verified':condition!='C3' or timing['latency_check_accepted']}
            write_json(results/(run_id+'-client.json'),entries)
            write_json(results/(run_id+'-snapshot.json'),job)
            write_json(results/(run_id+'-summary.json'),details)
            (results/(run_id+'-server.log')).write_text(config+'\n'+'\n'.join(lines)+'\n')
            csv_path=results/'runs.csv';new=not csv_path.exists()
            with csv_path.open('a',newline='') as out:
                writer=csv.DictWriter(out,fieldnames=COLUMNS)
                if new:writer.writeheader()
                writer.writerow(row)
            print(f"SAVED {run_id}: {row['requests_started']} GETs; detection {row['detection_ms']} ms; processing {row['processing_ms']} ms.",flush=True)
            if not all(details['validation'].values()) or 'REVIEW:' in row['notes']:
                raise RuntimeError('Saved evidence needs review; stopping before the next run. '+row['notes'])
        print('REQUESTED RUNS COMPLETE. Stopping for review; no other measured runs have been started.',flush=True)
        print('Results: '+str(results),flush=True)
    finally:
        if browser:browser.close()
        if server is not None and server.poll() is None:
            os.killpg(server.pid,signal.SIGINT)
            try:server.wait(timeout=15)
            except subprocess.TimeoutExpired:
                os.killpg(server.pid,signal.SIGTERM);server.wait(timeout=5)
        output.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--repetition',type=int,default=1,help='Pair number; alternate short-first and long-first automatically')
    parser.add_argument('--guided-mode', choices=('short','long'), help='With --guided-pair, record only this mode and then stop')
    parser.add_argument('--results-dir', type=Path, help='Separate results folder for this measured series')
    parser.add_argument('--prediction-file', type=Path, help='User prediction JSON saved before measured testing')
    parser.add_argument('--guided-pair',action='store_true',help='Run exactly one short and one long real-UI upload for the selected condition, then stop')
    parser.add_argument('--image',type=Path,default=Path.home()/'Pictures/ImageLab-Test-Images/VALID-beach.png')
    parser.add_argument('--condition', choices=PREDICTIONS, default='A')
    parser.add_argument('--server-log', type=Path, default=PROJECT/'assessment2-results/A-server.log')
    parser.add_argument('--url', default='http://localhost:4000')
    parser.add_argument('--latency-setting', default='No added latency; keep browser throttling disabled')
    parser.add_argument('--driver', default=shutil.which('geckodriver'))
    parser.add_argument('--self-check', action='store_true', help='Open Firefox and check the connection for 65 seconds; never upload an image')
    parser.add_argument('--check-only', action='store_true', help='Check local setup without opening a browser or uploading anything')
    args = parser.parse_args()
    if not args.driver:
        parser.error('Firefox geckodriver was not found')
    if args.guided_pair:
        guided_pair(args)
        return
    if not args.server_log.exists():
        parser.error('Server log not found: ' + str(args.server_log))
    if args.check_only:
        print('Ready: Firefox driver found; server log exists. No browser opened and no requests sent.')
        return
    results = HERE/'results'
    results.mkdir(exist_ok=True)
    if not args.self_check:
        write_json(results/f'session-{datetime.now().strftime("%Y%m%d-%H%M%S")}.json', {'condition':args.condition,'prediction':PREDICTIONS[args.condition],'written_at':datetime.now(timezone.utc).isoformat(),'latency_setting':args.latency_setting,'server_log':str(args.server_log),'url':args.url,'method':'Read existing in-page observation log and final job snapshot through WebDriver; no injected fetch wrapper and no extra job GETs. User starts every upload. A separate Firefox profile is used.'})
    browser = None
    active = None
    recorded = set()
    foreground = {}
    try:
        browser = Browser(args.driver)
        browser.command('/url', {'url':args.url})
        if args.self_check:
            print('CONNECTION CHECK ONLY — no uploads or measured runs will start.',flush=True)
            for second in range(66):
                state=browser.command('/execute/sync',{'script':"return {url:location.href, logReady:Array.isArray(window.imageLabObservationLog), uploads:document.querySelector('#file')?.files.length || 0, observations:(window.imageLabObservationLog || []).length};",'args':[]})
                if not state['logReady']:
                    raise RuntimeError('The app request log is not available. Check that ImageLab is running at '+args.url)
                if state['observations'] or state['uploads']:
                    raise RuntimeError('Self-check stopped: an image was selected or observation started. Do not upload during the connection check.')
                if second % 10 == 0: print(f'Connection OK at {second}s; zero selected images and zero observation requests.',flush=True)
                if second<65: time.sleep(1)
            print('PASS: Firefox connection stayed responsive for at least 65 seconds. No image job was created. File-saving on a real run still needs the guided pair.',flush=True)
            return
        print('\nRECORDER READY — Condition ' + args.condition, flush=True)
        print('Use the NEW Firefox window. Choose your image and mode, then click Process image yourself.', flush=True)
        print('Wait for SAVED here before starting the next job. No manual JSON or HAR downloads needed.', flush=True)
        print('Only manually started uploads are recorded. We will check one short and one long run before any batch.', flush=True)
        print('Ctrl+C stops this recorder and closes only its separate Firefox window.\n', flush=True)
        while True:
            snapshot = browser.command('/execute/sync', {'script': '''
                return {
                  job: window.eval("typeof activeJob === 'undefined' ? null : activeJob"),
                  entries: window.imageLabObservationLog || [],
                  image: document.querySelector('#file')?.files?.[0]?.name || '',
                  visible: document.visibilityState === 'visible',
                  href: window.location.href
                };
            ''', 'args':[]})
            job = snapshot.get('job')
            if job and job['id'] not in recorded:
                job_id = job['id']
                entries = [e for e in snapshot['entries'] if e.get('job_id') == job_id]
                if active != job_id:
                    if active and active not in recorded:
                        print('WARNING: previous job changed before its terminal record was saved. Do not count that run.', flush=True)
                    active = job_id
                    print('Recording job ' + job_id + ' — keep this app tab in the foreground.', flush=True)
                foreground[job_id] = foreground.get(job_id, True) and snapshot['visible']
                if job['status'] in ('completed', 'failed') and entries and not any(e['outcome']=='pending' for e in entries):
                    modes = {e['mode'] for e in entries}
                    mode = next(iter(modes)) if len(modes)==1 else 'mixed'
                    repetition = repetition_for(args.condition, mode, results)
                    run_id = f'{args.condition}-{mode}-{repetition}'
                    if any(results.glob(run_id+'-*')):
                        raise RuntimeError('Refusing to overwrite existing run: '+run_id)
                    lines, fields, config = server_records(args.server_log, job_id)
                    row, details = summarize(job, entries, fields, run_id, args.condition, repetition, snapshot['image'], args.latency_setting, foreground[job_id])
                    expected = {'A':('30s','0s'),'B':('0s','0s'),'C1':('0s','2s'),'C2':('0s','200ms'),'C3':('0s','200ms')}[args.condition]
                    if not all(text in config for text in (f'worker_delay={expected[0]}',f'simulation_interval={expected[1]}')):
                        warning = f' CONDITION CHECK NEEDED: expected delay={expected[0]}, simulation={expected[1]}; recorded startup: {config}'
                        row['notes']+=warning; details['notes']+=warning
                    write_json(results/(run_id+'-client.json'), entries)
                    write_json(results/(run_id+'-snapshot.json'),job)
                    write_json(results/(run_id+'-summary.json'),details)
                    (results/(run_id+'-server.log')).write_text(config+'\n'+'\n'.join(lines)+'\n')
                    csv_path = results/'runs.csv'
                    existed = csv_path.exists()
                    with csv_path.open('a',newline='') as output:
                        writer=csv.DictWriter(output,fieldnames=COLUMNS)
                        if not existed: writer.writeheader()
                        writer.writerow(row)
                    recorded.add(job_id)
                    print(f"SAVED {run_id}: {row['requests_started']} GETs; completion detection {row['detection_ms']} ms; processing {row['processing_ms']} ms.",flush=True)
                    if 'INCOMPLETE:' in row['notes'] or 'INVALID' in row['notes'] or 'CHECK NEEDED' in row['notes'] or 'REVIEW:' in row['notes']:
                        print('CHECK: '+row['notes'],flush=True)
                    print('Files: '+str(results)+'\nWait for guidance before starting another run.\n',flush=True)
            time.sleep(.25)
    except KeyboardInterrupt:
        print('\nRecorder stopped. ImageLab server is unchanged.',flush=True)
    finally:
        if browser:
            browser.close()


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print('Recorder error: '+str(error),file=sys.stderr)
        sys.exit(1)
