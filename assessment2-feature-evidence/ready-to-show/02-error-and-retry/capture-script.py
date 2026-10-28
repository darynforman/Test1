"""Capture real ImageLab lifecycle demonstrations; never writes experiment results.
Network retry fault: a local forwarding proxy closes status-GET connections.
Worker fault: temporarily rename only this session's newly uploaded copy.
Screenshots and animations show the unmodified UI; state is never fabricated.
"""
import sys,os,json,time,socket,threading,http.client,subprocess,signal,base64,csv,hashlib,shutil,traceback,tempfile
from pathlib import Path
from datetime import datetime,timezone
from http.server import ThreadingHTTPServer,BaseHTTPRequestHandler
from urllib.request import build_opener,ProxyHandler
from PIL import Image
BASE=Path('/home/daryn-forman/Documents/Test1')
sys.path.insert(0,str(BASE/'standalone-recorder'))
from record import Browser
IMAGE=Path('/home/daryn-forman/Pictures/ImageLab-Test-Images/VALID-beach.png')
DSN='host=/var/run/postgresql dbname=imagelab user=daryn-forman sslmode=disable'
SESSION=BASE/'assessment2-feature-evidence'/datetime.now().strftime('session-%Y%m%d-%H%M%S')
SESSION.mkdir(parents=True);shutil.copy2(__file__,SESSION/'capture.py')
RUNTIME=Path(tempfile.mkdtemp(prefix='imagelab-feature-'));STORAGE=RUNTIME/'storage'
BACKEND=4101;FRONT=4100
transport=[];transport_lock=threading.Lock();fault=threading.Event(); browser=None; app=None;proxy=None
results=[];frames=[];last_frame=0;case=None;events=[];held_files=[]
ms=lambda:int(time.time()*1000)

def write(p,value):p.write_text(json.dumps(value,indent=2)+'\n')
def sql(query):
 r=subprocess.run(['psql',DSN,'-X','-qAt','-v','ON_ERROR_STOP=1','-c',query],capture_output=True,text=True,check=True)
 return r.stdout.strip()

def code_hashes():
 ps=[p for directory in ['cmd','internal','web','migrations'] for p in (BASE/directory).rglob('*') if p.is_file()]
 ps+=[BASE/'Makefile',BASE/'standalone-recorder/results/restart-20261025-101935/runs.csv']
 return {str(p.relative_to(BASE)):hashlib.sha256(p.read_bytes()).hexdigest() for p in ps}

class Forwarder(BaseHTTPRequestHandler):
 protocol_version='HTTP/1.0'
 def log_message(self,*args):pass
 def do_GET(self):self.forward()
 def do_POST(self):self.forward()
 def forward(self):
  entry={'request_id':None,'method':self.command,'path':self.path,'start_ms':ms()}
  with transport_lock:entry['request_id']=len(transport)+1;transport.append(entry)
  if self.command=='GET' and self.path.startswith('/v1/jobs/') and fault.is_set():
   entry.update(outcome='deliberate_connection_close',end_ms=ms())
   try:self.connection.shutdown(socket.SHUT_RDWR)
   except OSError:pass
   self.connection.close();self.close_connection=True;return
  upstream=None
  try:
   raw=self.rfile.read(int(self.headers.get('Content-Length','0'))) if self.command=='POST' else None
   headers={k:v for k,v in self.headers.items() if k.lower() not in ['host','connection','transfer-encoding']}
   headers['Host']=f'localhost:{BACKEND}'
   upstream=http.client.HTTPConnection('127.0.0.1',BACKEND,timeout=45)
   upstream.request(self.command,self.path,body=raw,headers=headers);response=upstream.getresponse();body=response.read()
   entry.update(http_status=response.status,response_ms=ms(),outcome='upstream_response')
   if self.path.startswith('/v1/'):
    try:entry['response_json']=json.loads(body)
    except (ValueError,UnicodeError):pass
   self.send_response(response.status)
   for k,v in response.getheaders():
    if k.lower() not in ['connection','content-length','transfer-encoding']:self.send_header(k,v)
   self.send_header('Content-Length',str(len(body)));self.send_header('Connection','close');self.end_headers();self.wfile.write(body)
  except (BrokenPipeError,ConnectionResetError) as e:entry['downstream_disconnected']=True
  except Exception as e:
   entry.update(outcome='proxy_error',error=repr(e))
   try:self.send_error(502)
   except OSError:pass
  finally:
   entry['end_ms']=ms()
   if upstream:upstream.close()

def js(script,*args):return browser.command('/execute/sync',{'script':script,'args':list(args)})
def snap():
 return js("return {job:window.eval(\"typeof activeJob==='undefined'?null:activeJob\"),entries:window.imageLabObservationLog||[],visible:document.visibilityState==='visible',retrieval_error:!document.querySelector('#retrievalError').hidden,worker_error:!document.querySelector('#processingError').hidden,worker_error_text:document.querySelector('#processingError').textContent,upload_error:document.querySelector('#error').textContent,indicator:document.querySelector('#pollingIndicator').textContent,mode:document.querySelector('#observationMode').value,title:document.querySelector('#jobTitle').textContent};")

def capture(name=None):
 global last_frame
 now=ms()
 if name:
  full=base64.b64decode(browser.command('/moz/screenshot/full',method='GET'))
  (case/(name+'.png')).write_bytes(full)
 element=browser.command('/element',{'using':'css selector','value':'#jobCard'})
 panel=base64.b64decode(browser.command('/element/'+element['element-6066-11e4-a52e-4f735466cecf']+'/screenshot',method='GET'))
 (case/'frames').mkdir(exist_ok=True)
 file=case/(name+'-job-panel.png') if name else case/'frames'/f'frame-{len(frames):03}.png'
 file.write_bytes(panel);frames.append((file,now));last_frame=time.monotonic()
 if name:
  state=snap();write(case/(name+'-state.json'),state);print(case.name+': '+name+' | '+state['title'],flush=True)
 return file

def event(action,**extra):events.append({'time_ms':ms(),'action':action,**extra})
def wait_for(predicate,timeout=40):
 until=time.monotonic()+timeout
 while time.monotonic()<until:
  state=snap()
  if state['upload_error']:raise RuntimeError(state['upload_error'])
  if time.monotonic()-last_frame>=.8:capture()
  if predicate(state):return state
  time.sleep(.15)
 raise RuntimeError('Timed out waiting for real UI state: '+json.dumps(snap()))

def pause(seconds):
 until=time.monotonic()+seconds
 while time.monotonic()<until:
  if time.monotonic()-last_frame>=.8:capture()
  time.sleep(.15)

def click(selector):
 element=browser.command('/element',{'using':'css selector','value':selector})
 browser.command('/element/'+element['element-6066-11e4-a52e-4f735466cecf']+'/click',{})

def set_mode(mode):
 js("const s=document.querySelector('#observationMode');s.value=arguments[0];s.dispatchEvent(new Event('change',{bubbles:true}));",mode)
 event('change_polling_mode',mode=mode)

def start(name,mode):
 global case,frames,last_frame,events
 assert sql("SELECT count(*) FROM jobs WHERE status IN ('queued','processing')")=='0','Worker queue must be empty'
 case=SESSION/name;case.mkdir();frames=[];events=[];last_frame=0
 browser.command('/url',{'url':f'http://localhost:{FRONT}/'})
 wait_for(lambda s:s['job'] is None)
 set_mode(mode)
 file=browser.command('/element',{'using':'css selector','value':'#file'})
 browser.command('/element/'+file['element-6066-11e4-a52e-4f735466cecf']+'/value',{'text':str(IMAGE)})
 capture('01-ready');event('click_Process_image');click('#process')
 state=wait_for(lambda s:s['job'] and s['job']['status']=='processing')
 event('worker_processing_observed',job_id=state['job']['id']);capture('02-processing')
 return state['job']['id']

def finish(name,job_id,expected,extra):
 terminal=wait_for(lambda s:s['job'] and s['job']['id']==job_id and s['job']['status']==expected and not any(e['outcome']=='pending' for e in s['entries']))
 capture('terminal-'+expected);before=len(terminal['entries']);start_wait=ms();pause(6);after=snap();capture('polling-still-stopped')
 assert after['job']['id']==job_id and after['job']['status']==expected
 assert len(after['entries'])==before and not any(e['outcome']=='pending' for e in after['entries']), 'Polling did not stay stopped'
 assert after['indicator']=='Status checking stopped'
 if expected=='failed':
  assert after['worker_error'] and after['worker_error_text'] and after['job'].get('failed_at') and after['job'].get('error')
  assert not after['job'].get('variants') and not after['job'].get('completed_at')
 else:assert after['job']['variants_completed']==3 and len(after['job']['variants'])==3
 with transport_lock:
  own=[dict(x) for x in transport if '/v1/jobs/'+job_id in x['path'] or (x['method']=='POST' and x.get('response_json',{}).get('job_id')==job_id)]
 posts=[r for r in own if r['method']=='POST'];assert len(posts)==1,'An unexpected second upload occurred'
 assert all(e['job_id']==job_id for e in after['entries'])
 if expected=='failed':assert all('/v1/jobs/'+job_id not in r['path'] or r['start_ms']<=start_wait for r in own)
 stored=json.loads(sql("SELECT row_to_json(t) FROM (SELECT public_id,status,version,variants_completed,error_message,started_at,completed_at,failed_at FROM jobs WHERE public_id='"+job_id+"')t"))
 assert stored['status']==expected and stored['version']==after['job']['version']
 result={'demonstration':name,'job_id':job_id,'observed_terminal_state':expected,'real_browser_UI':True,'source_unchanged':None,'upload_POSTs':len(posts),'browser_requests_at_terminal':before,'browser_requests_after_six_seconds':len(after['entries']),'stop_observed_for_ms':ms()-start_wait,'no_pending_requests':True,'same_job_throughout':True,'checks':extra,'database_terminal_record':stored,'recording':'Time-sampled actual browser screenshots; GIF frame durations follow capture timestamps. Not a full-frame-rate video.'}
 write(case/'verification.json',result);write(case/'client-log.json',after['entries']);write(case/'terminal-ui-state.json',after);write(case/'network-log.json',own);write(case/'actions.json',events)
 with (case/'network-requests.csv').open('w',newline='') as h:
  w=csv.DictWriter(h,fieldnames=['request_id','method','path','start_ms','response_ms','end_ms','http_status','outcome','downstream_disconnected'],extrasaction='ignore');w.writeheader();w.writerows(own)
 lines=[l for l in (SESSION/'server.log').read_text().splitlines() if f'job_id={job_id} 'in l]
 (case/'server-job.log').write_text('\n'.join(lines)+'\n')
 assert any('status='+expected in line and 'msg=job_commit' in line for line in lines)
 # Save actual captured frames as a convenient animated screenshot sequence.
 ims=[];durations=[]
 images=[Image.open(path).convert('RGB') for path,t in frames]
 maxw=max(im.width for im in images);maxh=max(im.height for im in images)
 for i,((path,t),im) in enumerate(zip(frames,images)):
  canvas=Image.new('RGB',(maxw,maxh),'white');canvas.paste(im,(0,0));ims.append(canvas.convert('P',palette=Image.Palette.ADAPTIVE))
  durations.append(max(40,frames[i+1][1]-t) if i+1<len(frames) else 1200)
 ims[0].save(case/'demonstration.gif',save_all=True,append_images=ims[1:],duration=durations,loop=0,optimize=True)
 write(case/'screen-capture-timestamps.json',[{'file':str(p.relative_to(case)),'capture_ms':t,'display_duration_ms':d}for(p,t),d in zip(frames,durations)])
 results.append(result);write(SESSION/'verification.json',{'status':'in_progress','demonstrations':results})
 print('VERIFIED '+name+' | one POST | same job | '+expected+' | no further polls for 6 s',flush=True)

baseline=code_hashes();write(SESSION/'source-and-data-before.json',baseline)
try:
 assert sql("SELECT count(*) FROM jobs WHERE status IN ('queued','processing')")=='0'
 # Refuse to use occupied ports; no existing server is stopped.
 for port in [BACKEND,FRONT]:
  sock=socket.socket();sock.bind(('127.0.0.1',port));sock.close()
 config={'purpose':'Separate lifecycle demonstrations, not additional experimental runs','image':str(IMAGE),'image_sha256':hashlib.sha256(IMAGE.read_bytes()).hexdigest(),'worker_delay':'15s','simulation_interval':'0s','app_port':BACKEND,'browser_forwarder_port':FRONT,'storage':str(STORAGE),'network_fault':'External localhost forwarder deliberately closes status-GET connection. Real Go application handles all successful requests; no fake snapshots or fetch replacement.','failure_fault':'Temporarily rename only the newly uploaded demonstration copy, then restore after real worker failure.','proxy_limitation':'During mode cancellation, the browser abort is real; this simple forwarder may keep its upstream request open until the next server response. These captures do not test backend cancellation cleanup.','screenshots':'Actual Firefox full-page and native job-panel screenshots, plus raw logs and time-sampled job-panel GIF sequences. White padding only equalises frame dimensions.'}
 write(SESSION/'configuration.json',config)
 app_log=(SESSION/'server.log').open('w')
 app=subprocess.Popen(['/tmp/imagelab-feature-tools/imagelab','-db-dsn='+DSN,'-port='+str(BACKEND),'-worker-delay=15s','-simulation-interval=0s','-storage-dir='+str(STORAGE)],cwd=BASE,stdout=app_log,stderr=subprocess.STDOUT)
 opener=build_opener(ProxyHandler({}))
 for _ in range(100):
  if app.poll() is not None:raise RuntimeError((SESSION/'server.log').read_text())
  try:
   with opener.open(f'http://localhost:{BACKEND}/v1/healthcheck',timeout=1):break
  except OSError:time.sleep(.1)
 else:raise RuntimeError('App did not start')
 proxy=ThreadingHTTPServer(('127.0.0.1',FRONT),Forwarder);proxy.daemon_threads=True;threading.Thread(target=proxy.serve_forever,daemon=True).start()
 browser=Browser('/snap/bin/geckodriver')
 browser.command('/window/rect',{'width':1440,'height':1020})
 write(SESSION/'browser.json',browser.capabilities)
 print('Capturing real browser at localhost:'+str(FRONT)+'; output '+str(SESSION),flush=True)
 job=start('01-mode-switch-worker-continues','long')
 wait_for(lambda s:any(e['outcome']=='pending' and e['after']==s['job']['version'] for e in s['entries']))
 capture('03-long-poll-pending');set_mode('short')
 short=wait_for(lambda s:any(e['mode']=='short' and e['outcome']=='initial_200' for e in s['entries']))
 assert short['job']['id']==job and short['job']['status']=='processing';assert any(e['outcome']=='cancelled' and e['mode']=='long' for e in short['entries'])
 capture('04-switched-to-short');pause(2.1);set_mode('long')
 switched=wait_for(lambda s:s['mode']=='long' and any(e['outcome']=='pending' and e['mode']=='long' for e in s['entries']))
 assert switched['job']['id']==job and switched['job']['status']=='processing';capture('05-switched-back-to-long')
 finish('mode_switch',job,'completed',{'long_to_short_while_processing':True,'short_to_long_while_processing':True,'old_long_poll_cancelled':True,'final_three_variants':True})
 job=start('02-network-error-try-again','short');before=snap();fault.set();event('enable_status_connection_fault',job_id=job)
 error=wait_for(lambda s:s['retrieval_error'])
 assert error['job']['id']==job and error['job']['status']=='processing' and error['job']['version']==before['job']['version']
 assert any(e['outcome']=='retrieval_error' for e in error['entries']);capture('03-real-network-error');n=len(error['entries']);pause(2.2);assert len(snap()['entries'])==n
 fault.clear();event('disable_status_connection_fault');event('click_Try_again');click('#retry')
 recovered=wait_for(lambda s:not s['retrieval_error'] and len(s['entries'])>n and s['entries'][-1]['outcome']!='pending')
 assert recovered['job']['id']==job;capture('04-retrying-same-job')
 finish('network_error_retry',job,'completed',{'real_status_fetch_failed':True,'error_preserved_last_known_job_and_version':True,'polling_paused_until_Try_again':True,'same_job_retry_via_actual_button':True,'no_second_POST':True})
 for mode in ['long','short']:
  job=start('03-worker-failure-'+mode if mode=='long' else '04-worker-failure-'+mode,mode)
  stored=sql("SELECT i.stored_filename FROM images i JOIN jobs j ON j.image_id=i.id WHERE j.public_id='"+job+"'")
  assert stored and Path(stored).name==stored
  original=STORAGE/'originals'/stored;held=original.with_suffix(original.suffix+'.temporarily-held')
  assert original.exists() and hashlib.sha256(original.read_bytes()).hexdigest()==config['image_sha256']
  original.rename(held);held_files.append((held,original));event('temporarily_rename_demo_copy',job_id=job,stored_filename=stored)
  capture('03-waiting-for-real-worker-failure')
  finish('worker_failure_'+mode,job,'failed',{'injected_fault':'Only this new demo copy temporarily absent when worker opens it','worker_persisted_failure':True,'browser_showed_failed_and_safe_error':True,'polling_stopped_in_mode':mode,'no_completed_results':True})
  held.rename(original);held_files.remove((held,original));event('restore_demo_copy',job_id=job);write(case/'actions.json',events)
 after=code_hashes();assert after==baseline,'Source or experimental CSV changed'
 for r in results:r['source_unchanged']=True
 write(SESSION/'source-and-data-after.json',after)
 write(SESSION/'verification.json',{'status':'completed','verified_demonstrations':4,'application_source_unchanged':True,'experiment_runs_csv_unchanged':True,'demonstrations':results,'scope':'Only the named lifecycle checks were exercised. Not a blanket acceptance-test pass.','evidence':config})
 print('ALL FOUR LIFECYCLE DEMONSTRATIONS VERIFIED. Evidence: '+str(SESSION),flush=True)
except BaseException as exc:
 write(SESSION/'incomplete.json',{'error':repr(exc),'traceback':traceback.format_exc(),'completed_demonstrations':results})
 print('INCOMPLETE: '+repr(exc)+'; no unverified demonstration declared successful. See '+str(SESSION),flush=True)
 raise
finally:
 fault.clear()
 for held,original in held_files:
  if held.exists() and not original.exists():held.rename(original)
 if browser:browser.close()
 if proxy:proxy.shutdown();proxy.server_close()
 if app and app.poll() is None:
  app.send_signal(signal.SIGTERM)
  try:app.wait(timeout=40)
  except subprocess.TimeoutExpired:app.terminate()
 write(SESSION/'all-network-log.json',transport)
 if 'app_log' in globals():app_log.close()
