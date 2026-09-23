// Automated frontend tests for app.js. Run with: make test-ui
// Uses a simulated page, server replies, and timers; no real server is needed.
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');

// A small DOM and controllable clock keep polling and cancellation tests deterministic.
function setup(){
 // Implement the page element properties and methods that app.js uses.
 class Element {
  constructor(){this.hidden=false;this.disabled=false;this.textContent='';this.style={};this.dataset={};this.children=[];this.listeners={};this.classList={add(){},remove(){}};}
  addEventListener(name,fn){this.listeners[name]=fn;}
  querySelector(){return this.span??=new Element();}
  removeAttribute(name){delete this[name];}
  replaceChildren(...children){this.children=children;}
  append(...children){this.children.push(...children);}
 }
 // Reuse the same fake element whenever the app selects it again.
 const elements=new Map();
 const el=id=>{if(!elements.has(id))elements.set(id,new Element());return elements.get(id);};
 // Store timer callbacks so tests can trigger them without waiting in real time.
 const timers=new Map();let sequence=0;
 // Record outgoing requests and queue fake server replies for each test.
 const requests=[];const responses=[];const events={};
 class LocalURL extends URL {static createObjectURL(){return 'blob:preview';}static revokeObjectURL(){}}
 // Give the frontend an isolated environment with fake browser APIs.
 const context=vm.createContext({
  document:{querySelector:el,createElement:()=>new Element()},
  window:{location:{origin:'http://localhost:4000'},addEventListener:(name,fn)=>events[name]=fn},
  URL:LocalURL,AbortController,FormData:class {append(){}},console,
  setTimeout:(fn,delay)=>{const id=++sequence;timers.set(id,{fn,delay});return id;},
  clearTimeout:id=>timers.delete(id),
  fetch:(url,options={})=>{
   requests.push({url,options});
   const next=responses.shift();
   if(!next)throw Error('Unexpected fetch');
   return typeof next==='function'?next(options):Promise.resolve(next);
  }
 });
 // Load the actual frontend code and register its event handlers.
 vm.runInContext(fs.readFileSync(path.join(__dirname,'../assets/app.js'),'utf8'),context);
 const run=code=>vm.runInContext(code,context);
 // Execute one pending timer with this delay, such as the 1000ms status check.
 const tick=async delay=>{
  const entry=[...timers].find(([,t])=>t.delay===delay);
  assert.ok(entry,`expected ${delay}ms timer`);
  timers.delete(entry[0]);await entry[1].fn();
 };
 // Create a fake fetch response with the response methods used by app.js.
 const reply=(job,status=200)=>({ok:status>=200&&status<300,status,json:async()=>job,text:async()=>JSON.stringify(job)});
 // Simulate choosing a file, clicking Process, and receiving HTTP 202 acceptance.
 const accept=async(id='job-a')=>{
  responses.push(reply({job_id:id,image_id:'image-a',status:'queued',status_url:'/v1/jobs/'+id},202));
  run("choose({name:'photo.png',type:'image/png',size:100})");
  await el('#process').listeners.click();
 };
 return {el,run,tick,requests,responses,timers,reply,accept,events};
}
// Build sample job replies. These dimensions are test data, not actual resize settings.
function job(status='processing',id='job-a'){
 const data={id,image_id:'image-a',status,queued_at:'2026-09-20T10:00:00Z',started_at:status==='queued'?null:'2026-09-20T10:00:01Z',completed_at:null,failed_at:null};
 if(status==='completed'){
  data.completed_at='2026-09-20T10:00:02Z';
  data.variants=['thumbnail','preview','display'].map(name=>({name,width:150,height:150,url:`/v1/images/image-a/variants/${name}`}));
 }
 if(status==='failed'){data.failed_at='2026-09-20T10:00:02Z';data.error='Image processing could not finish. Please submit the image again.';}
 return data;
}

// Check the normal flow: selection, upload, polling, and displaying completed variants.
test('selection sends nothing; accepted upload polls once per second and stops with three results',async()=>{
 const h=setup();h.run("choose({name:'photo.png',type:'image/png',size:100})");
 assert.equal(h.requests.length,0);assert.equal(h.timers.size,0);
 await h.accept();assert.equal(h.requests.length,1);assert.equal(h.el('#variantCards').hidden,true);
 for(const state of ['queued','processing','completed']){
  h.responses.push(h.reply(job(state)));await h.tick(1000);
  assert.equal(h.el('#statusBadge').textContent,state);
 }
 assert.equal(h.requests.length,4);assert.equal(h.timers.size,0);
 assert.equal(h.el('#variantCards').children.length,3);assert.equal(h.el('#results').hidden,true);
 assert.equal(h.el('#stepComplete').dataset.state,'done');
});

// A failed job shows failure details, hides image results, and stops polling.
test('processing failure displays the safe error and stops without results',async()=>{
 const h=setup();await h.accept();h.responses.push(h.reply(job('failed')));await h.tick(1000);
 assert.equal(h.el('#jobTitle').textContent,'Processing failed');
 assert.equal(h.el('#processingError').hidden,false);
 assert.equal(h.el('#failedAt').textContent.includes('2026'),true);
 assert.equal(h.el('#stepComplete').dataset.state,'pending');
 assert.equal(h.el('#variantCards').hidden,true);assert.equal(h.timers.size,0);
});

// Status retrieval problems must preserve the last known job state.
// Retrying should check the existing job without uploading the image again.
for(const failure of ['network','http','invalid-json','wrong-job','missing-variant','timeout']){
 test(`${failure} preserves last state; Try again observes the same job without POST`,async()=>{
  const h=setup();await h.accept();h.responses.push(h.reply(job()));await h.tick(1000);
  if(failure==='network')h.responses.push(()=>Promise.reject(Error('offline')));
  if(failure==='http')h.responses.push(h.reply({},503));
  if(failure==='invalid-json')h.responses.push({ok:true,json:async()=>{throw Error('bad JSON');}});
  if(failure==='wrong-job')h.responses.push(h.reply(job('failed','other-job')));
  if(failure==='missing-variant'){const bad=job('completed');bad.variants.pop();h.responses.push(h.reply(bad));}
  if(failure==='timeout')h.responses.push(({signal})=>new Promise((resolve,reject)=>signal.addEventListener('abort',()=>reject(Error('timeout')))));
  const pending=h.tick(1000);
  if(failure==='timeout')await h.tick(10000);
  await pending;
  assert.equal(h.el('#statusBadge').textContent,'processing');
  assert.equal(h.el('#retrievalError').hidden,false);assert.equal(h.el('#processingError').hidden,true);assert.equal(h.timers.size,0);
  h.responses.push(h.reply(job('completed')));h.el('#retry').listeners.click();await h.tick(0);
  assert.equal(h.requests.filter(r=>r.options.method==='POST').length,1);
  assert.equal(h.requests.at(-1).url,'/v1/jobs/job-a');assert.equal(h.el('#statusBadge').textContent,'completed');
  assert.equal(h.el('#retrievalError').hidden,true);assert.equal(h.timers.size,0);
 });
}

// Hold an upload open to check that repeated clicks cannot send duplicate uploads.
test('rapid repeated clicks cannot overlap uploads',async()=>{
 const h=setup();let resolve;
 h.responses.push(()=>new Promise(r=>resolve=r));h.run("choose({name:'photo.png',type:'image/png',size:100})");
 const pending=h.el('#process').listeners.click();await h.el('#process').listeners.click();
 assert.equal(h.requests.length,1);assert.equal(h.el('#process').disabled,true);
 resolve(h.reply({error:'Upload rejected'},400));await pending;
 assert.equal(h.el('#process').disabled,false);assert.equal(h.timers.size,0);
});

// A late response from an old job must not overwrite the new job's displayed status.
test('new upload aborts old observation and ignores a late response',async()=>{
 const h=setup();await h.accept();let resolve;
 h.responses.push(()=>new Promise(r=>resolve=r));const pending=h.tick(1000);
 assert.equal([...h.timers.values()].some(t=>t.delay===1000),false);
 const oldRequest=h.requests.at(-1);await h.accept('job-b');
 assert.equal(oldRequest.options.signal.aborted,true);
 resolve(h.reply(job('failed')));await pending;
 assert.equal(h.el('#jobText').textContent,'Job job-b');assert.equal(h.el('#statusBadge').textContent,'queued');
 assert.equal(h.el('#processingError').hidden,true);
});

// Leaving the page cancels the active status request and prevents further polling.
test('page shutdown aborts an active request and stops all polling',async()=>{
 const h=setup();await h.accept();let resolve;
 h.responses.push(()=>new Promise(r=>resolve=r));const pending=h.tick(1000);
 const request=h.requests.at(-1);h.events.pagehide();
 assert.equal(request.options.signal.aborted,true);
 resolve(h.reply(job('completed')));await pending;assert.equal(h.timers.size,0);
 assert.equal(h.el('#variantCards').hidden,true);
});

// Retrying an unfinished job should resume exactly one polling loop.
test('Try again resumes the one-second loop when the job is still active',async()=>{
 const h=setup();await h.accept();h.responses.push(h.reply({},500));await h.tick(1000);
 h.responses.push(h.reply(job()));h.el('#retry').listeners.click();await h.tick(0);
 assert.equal(h.el('#statusBadge').textContent,'processing');
 assert.equal(h.el('#retrievalError').hidden,true);
 assert.equal([...h.timers.values()].filter(t=>t.delay===1000).length,1);
 assert.equal(h.requests.filter(r=>r.options.method==='POST').length,1);
});

// An upload reply arriving after the page closes must not start status polling.
test('a page closed during upload never starts polling after acceptance',async()=>{
 const h=setup();let resolve;
 h.responses.push(()=>new Promise(r=>resolve=r));h.run("choose({name:'photo.png',type:'image/png',size:100})");
 const pending=h.el('#process').listeners.click();h.events.pagehide();
 resolve(h.reply({job_id:'job-a',image_id:'image-a',status:'queued',status_url:'/v1/jobs/job-a'},202));await pending;
 assert.equal(h.timers.size,0);assert.equal(h.requests.length,1);
});
