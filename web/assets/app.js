// Browser workflow: choose a local image -> upload it -> receive 202 Accepted ->
// poll the job URL -> show completed variants or a processing failure.
// The Go worker creates the images; this file controls what the browser displays.

// Keep references to the upload controls so functions can update the page.
const fileInput=document.querySelector('#file');
const dropzone=document.querySelector('#dropzone');
const empty=document.querySelector('#empty');
const selected=document.querySelector('#selected');
const preview=document.querySelector('#preview');
const filename=document.querySelector('#filename');
const filemeta=document.querySelector('#filemeta');
const processButton=document.querySelector('#process');
const errorBox=document.querySelector('#error');
let chosenFile=null;
let previewURL=null;
// This guard prevents another POST while the current upload is still pending.
let isSubmitting=false;

// Remove the local selection and preview, release its temporary browser URL,
// and disable processing until another acceptable image is selected.
function clearSelection(){
	chosenFile=null;
	if(previewURL){URL.revokeObjectURL(previewURL);previewURL=null;}
	preview.removeAttribute('src');
	filename.textContent='';
	filemeta.textContent='';
	selected.hidden=true;
	selected.style.display='none';
	empty.hidden=false;
	processButton.disabled=true;
}

// Check the selected file's reported type and size, then show its local preview
// and details. This sends no request; the Go server validates the upload again.
function choose(file){
	errorBox.textContent='';
	if(!file)return;
	// Explain a wrong file type separately from an image that is too large.
	if(!['image/jpeg','image/png'].includes(file.type)){
		clearSelection();
		fileInput.value='';
		errorBox.textContent='Unsupported file type. Please choose a JPEG or PNG image.';
		return;
	}
	// File sizes are in bytes; this is the 10 MB limit. The server checks it too.
	if(file.size>10*1024*1024){
		clearSelection();
		fileInput.value='';
		errorBox.textContent='This image is too large. Please choose an image no larger than 10 MB.';
		return;
	}
	chosenFile=file;
	if(previewURL)URL.revokeObjectURL(previewURL);
	// The object URL previews the local file without uploading it.
	previewURL=URL.createObjectURL(file);
	preview.src=previewURL;
	filename.textContent=file.name;
	const displaySize=file.size<1048576?`${Math.max(1,Math.round(file.size/1024))} KB`:`${(file.size/1048576).toFixed(1)} MB`;
	filemeta.textContent=`${displaySize} · ${file.type}`;
	empty.hidden=true;
	selected.hidden=false;
	selected.style.display='flex';
	processButton.disabled=isSubmitting;
}

// Connect clicking, replacing, and drag-and-drop to the same choose() function.
// Prevent the browser's default drag behavior so dropping a file selects it here.
dropzone.addEventListener('click',event=>{if(!event.target.closest('#replace'))fileInput.click();});
document.querySelector('#replace').addEventListener('click',event=>{event.stopPropagation();fileInput.click();});
fileInput.addEventListener('change',()=>choose(fileInput.files[0]));
['dragenter','dragover'].forEach(name=>dropzone.addEventListener(name,event=>{event.preventDefault();dropzone.classList.add('drag');}));
['dragleave','drop'].forEach(name=>dropzone.addEventListener(name,event=>{event.preventDefault();dropzone.classList.remove('drag');}));
dropzone.addEventListener('drop',event=>choose(event.dataTransfer.files[0]));

// Upload the selected image as multipart form data when Process image is clicked.
// Stop watching the previous job, then wait for a valid 202 response before
// displaying the new queued job and starting automatic status checks.
processButton.addEventListener('click',async()=>{
	if(isSubmitting||!chosenFile)return;
	// Lock immediately, before fetch, so rapid clicks cannot overlap requests.
	isSubmitting=true;
	stopObservation();
	activeJob=null;
	resetJobDisplay();
	processButton.disabled=true;
	processButton.textContent='Uploading...';
	errorBox.textContent='';
	try{
		const body=new FormData();
		body.append('image',chosenFile);
		const response=await fetch('/v1/images',{method:'POST',body});
		const responseText=await response.text();
		let data={};
		try{data=responseText?JSON.parse(responseText):{};}catch{throw new Error('The upload API returned an invalid response. Start and open the Go application on port 4000.');}
		if(!responseText)throw new Error('The upload API is unavailable. Start and open the Go application on port 4000.');
		// Expect acceptance, not completion; the background worker still handles the job.
		if(response.status!==202)throw new Error(data.error||'Upload was rejected.');
		if(typeof data.job_id!=='string'||typeof data.image_id!=='string'||data.status!=='queued'||!safePath(data.status_url,`/v1/jobs/${data.job_id}`)){
			throw new Error('The upload response is missing valid job details.');
		}
		if(pageClosed)return;
		activeJob={id:data.job_id,image_id:data.image_id,status:data.status,status_url:data.status_url};
		renderJob();
		startObservation(true);
	}catch(error){
		errorBox.textContent=error.message;
	}finally{
		isSubmitting=false;
		processButton.disabled=!chosenFile;
		processButton.textContent='⇧  Process image';
	}
});

// References to the job progress, error, and generated-image areas of the page.
const jobTitle=document.querySelector('#jobTitle');
const jobText=document.querySelector('#jobText');
const statusBadge=document.querySelector('#statusBadge');
const jobDetails=document.querySelector('#jobDetails');
const pollingIndicator=document.querySelector('#pollingIndicator');
const retrievalError=document.querySelector('#retrievalError');
const processingError=document.querySelector('#processingError');
const retryButton=document.querySelector('#retry');
const results=document.querySelector('#results');
const variantCards=document.querySelector('#variantCards');
let activeJob=null; // The accepted job and its most recently retrieved state.
let observation=null; // The current polling session: its timer and AbortController.
const modeControl=document.querySelector('#observationMode');
modeControl.value='short';
const observationLog=[];
window.imageLabObservationLog=observationLog;
let requestSequence=0;
let pageClosed=false; // Prevent late responses from starting polling after leaving.

// Accept only a URL on this app's origin with the exact expected API path.
// This checks job-status and image links before the browser uses them.
function safePath(value,expected){
	if(typeof value!=='string')return false;
	try{
		const url=new URL(value,window.location.origin);
		return url.origin===window.location.origin&&url.pathname===expected&&!url.search&&!url.hash;
	}catch{return false;}
}

// Replace any old image cards with a message, such as waiting for results or
// processing failed. Cards stay hidden until a completed job is displayed.
function resultsMessage(title,message){
	variantCards.replaceChildren();
	variantCards.hidden=true;
	results.hidden=false;
	document.querySelector('#resultsTitle').textContent=title;
	document.querySelector('#resultsText').textContent=message;
}

// Clear the previous job's visible details, errors, and results for a new upload.
// The upload handler separately clears activeJob and stops its polling session.
function resetJobDisplay(){
	jobTitle.textContent='No active job';
	jobText.textContent='Upload an image to create a processing job';
	statusBadge.hidden=true;
	jobDetails.hidden=true;
	retrievalError.hidden=true;
	processingError.hidden=true;
	resultsMessage('No images generated yet','Processed image variants will appear here');
}

// Update one timeline step's label and data-state attribute (used by the CSS).
function setStep(id,state){
	const step=document.querySelector(id);
	step.dataset.state=state;
	const label=step.querySelector('span');
	label.textContent={done:'✓',pending:'Pending',active:'In progress',failed:'Failed'}[state];
	label.title=state==='done'?'Done':'';
}

// Display activeJob: status badge, timeline, timestamps, and any processing error.
// Acceptance means the original is stored; only completed allows result cards.
function renderJob(){
	const job=activeJob;
	jobDetails.hidden=false;
	statusBadge.hidden=false;
	statusBadge.textContent=job.status;
	statusBadge.dataset.status=job.status;
	jobTitle.textContent={queued:'Upload accepted',processing:'Processing image',completed:'Processing complete',failed:'Processing failed'}[job.status];
	jobText.textContent=`Job ${job.id}`;
 document.querySelector('#jobProgress').textContent=Number.isSafeInteger(job.version)?`Version ${job.version} · ${job.variants_completed}/3 variants · ${job.stage}${job.simulation_step>0?` · Simulated progress: step ${job.simulation_step}`:''}`:'Waiting for initial snapshot';
	setStep('#stepAccepted','done');
	setStep('#stepStored','done');
	setStep('#stepGenerating',{queued:'pending',processing:'active',completed:'done',failed:'failed'}[job.status]);
	setStep('#stepComplete',job.status==='completed'?'done':'pending');
	for(const [element,field] of [['queuedAt','queued_at'],['startedAt','started_at'],['completedAt','completed_at'],['failedAt','failed_at']]){
		document.querySelector(`#${element}`).textContent=job[field]?new Date(job[field]).toLocaleString():'—';
	}
	processingError.hidden=job.status!=='failed';
	if(job.status==='failed'){
		processingError.textContent=job.error||'Image processing could not finish. Please submit the image again.';
		resultsMessage('No images generated','Processing failed. Please try uploading the image again.');
	}else if(job.status==='completed'){
		renderVariants(job.variants);
	}else{
		resultsMessage('Images are being generated','Results will appear when processing completes.');
	}
}

// Build a card for each completed output: thumbnail, preview, and display.
// Each card shows the image, actual dimensions, and View/Download links.
function renderVariants(variants){
	variantCards.replaceChildren();
	for(const name of ['thumbnail','preview','display']){
		const variant=variants.find(item=>item.name===name);
		const card=document.createElement('article');
		card.className='variant-card';
		const image=document.createElement('img');
		image.src=variant.url;
		image.alt=`${name} variant`;
		const title=document.createElement('h3');
		title.textContent=name;
		const dimensions=document.createElement('p');
		dimensions.textContent=`${variant.width} × ${variant.height} pixels`;
		const view=document.createElement('a');
		view.href=variant.url;
		view.target='_blank';
		view.rel='noopener';
		view.textContent='View';
		const download=document.createElement('a');
		download.href=variant.url;
		download.download=`${name}.jpg`;
		download.textContent='Download';
		card.append(image,title,dimensions,view,download);
		variantCards.append(card);
	}
	results.hidden=true;
	variantCards.hidden=false;
}

// Check that a status response belongs to the job being watched and contains
// a supported state and the required timestamps. Completed jobs must also have
// all three variants with valid dimensions and URLs before we display results.
// Unusable status responses are observation errors, never processing failures.
function validJob(job,previous){
	if(!job||!Number.isSafeInteger(job.version)||job.version<1||(previous.version!=null&&job.version<previous.version)||typeof job.stage!=='string'||!job.stage||!Number.isInteger(job.variants_completed)||job.variants_completed<0||job.variants_completed>3||!Number.isInteger(job.simulation_step)||job.simulation_step<0||job.id!==previous.id||job.image_id!==previous.image_id||!['queued','processing','completed','failed'].includes(job.status))return false;
	for(const field of ['queued_at','started_at','completed_at','failed_at']){
		if(job[field]!=null&&(typeof job[field]!=='string'||!Number.isFinite(Date.parse(job[field]))))return false;
	}
	if(!job.queued_at||(job.status!=='queued'&&!job.started_at))return false;
	if(job.status==='failed'&&(!job.failed_at||(job.error!=null&&typeof job.error!=='string')))return false;
	if(job.status==='completed'){
		if(job.variants_completed!==3||!job.completed_at||!Array.isArray(job.variants)||job.variants.length!==3)return false;
		return ['thumbnail','preview','display'].every(name=>{
			const v=job.variants.find(item=>item&&item.name===name);
			return v&&Number.isInteger(v.width)&&v.width>0&&Number.isInteger(v.height)&&v.height>0&&safePath(v.url,`/v1/images/${job.image_id}/variants/${name}`);
		});
	}
	return true;
}

// Cancel the next scheduled status check and abort any current status request.
// This only stops browser observation; the server's worker keeps processing.
function stopObservation(){
	if(!observation)return;
	clearTimeout(observation.timer);
	for(const entry of observationLog){if(entry.outcome==='pending'){entry.outcome='cancelled';entry.end_ms=Date.now();}}
 observation.controller.abort();
	observation=null;
}

// Each new session starts with a prompt snapshot, including after retry or switching.
function startObservation(immediate=true){
 // Stop the old loop first, so changing modes does not leave two browser requests running.
 stopObservation();
 if(!activeJob||pageClosed)return;
 retrievalError.hidden=true;
 const mode=modeControl.value;
 pollingIndicator.textContent=mode==='long'?'Long polling · Up to 20 seconds per request':'Short polling · 1 second after each response';
 const session={controller:new AbortController(),timer:null,mode,initial:true};
 observation=session;
 // Start now. The first GET reads the current job without asking the server to wait.
 session.timer=setTimeout(()=>pollJob(session),0);
}

// Both modes use this same loop. They differ in the request URL and when we check again.
async function pollJob(session){
 // Ignore a loop that belongs to an old job or an old mode selection.
 if(observation!==session)return;
 let timedOut=false;
 // The browser allows 30 seconds, longer than the server's 20-second holding deadline.
 const timeout=setTimeout(()=>{timedOut=true;session.controller.abort();},30000);
 // Even in long mode, the first request is prompt so we learn the starting version.
 const isLong=session.mode==='long'&&!session.initial;
 // SHORT: plain job URL, so the server replies with the current snapshot immediately.
 // LONG: "after" is our last version; "wait=20" lets the server hold an unchanged job.
 const url=activeJob.status_url+(isLong?`?after=${activeJob.version}&wait=20`:'');
 const entry={request_id:++requestSequence,job_id:activeJob.id,mode:session.mode,initial:session.initial,after:isLong?activeJob.version:null,start_ms:Date.now(),outcome:'pending'};
 observationLog.push(entry);
 // SHORT: wait 1 second AFTER the response and rendering, not between request starts.
 // LONG: schedule the next request immediately. We only call this after handling a reply.
 const schedule=()=>{session.timer=setTimeout(()=>pollJob(session),session.mode==='long'?0:1000);};
 try{
  const response=await fetch(url,{signal:session.controller.signal,cache:'no-store'});
  entry.receipt_ms=Date.now(); entry.http_status=response.status;
  // A mode switch may have happened while fetch was waiting. Do not use an old reply.
  if(observation!==session){entry.outcome='cancelled';return;}
  if(timedOut)throw new Error('Client timeout');
  if(response.status===204){
   if(!isLong)throw new Error('Unexpected no-content snapshot');
   // Normal long-poll expiry: no new snapshot, so keep our version and ask again.
   // A 204 has no JSON body; it is not a connection error or a failed image job.
   entry.outcome='no_change_204'; schedule(); return;
  }
  if(response.status!==200)throw new Error('Status retrieval failed');
  const job=await response.json();
  entry.receipt_ms=Date.now();
  // Check again because reading the JSON also takes time and the session could change.
  if(observation!==session){entry.outcome='cancelled';return;}
  if(timedOut||!validJob(job,activeJob)||(isLong&&job.version===activeJob.version&&['queued','processing'].includes(job.status)))throw new Error('Unusable status response');
  entry.version=job.version;
  entry.outcome=session.initial?'initial_200':job.version>activeJob.version?'newer_200':'unchanged_200';
  // The first received version is our baseline. Later requests use the latest version.
  session.initial=false;
  activeJob={...job,status_url:activeJob.status_url};
  // Both modes display the same job fields through the same rendering function.
  renderJob(); entry.render_ms=Date.now();
  // A completed or failed job is finished: display it and stop checking in BOTH modes.
  if(job.status==='completed'||job.status==='failed'){
   stopObservation(); pollingIndicator.textContent='Status checking stopped';
  }else{schedule();}
 }catch(error){
  entry.end_ms=Date.now();
  if(observation!==session){entry.outcome='cancelled';return;}
  entry.outcome=timedOut?'timeout':'retrieval_error';
  // A retrieval error does not prove worker failure. Keep the last state and offer Try again.
  stopObservation(); pollingIndicator.textContent='Status checking paused'; retrievalError.hidden=false;
 }finally{clearTimeout(timeout);if(!entry.end_ms)entry.end_ms=Date.now();}
}

// Switching modes starts a fresh observation of the SAME job, without another upload.
modeControl.addEventListener('change',()=>startObservation(true));
document.querySelector('#stopObserving').addEventListener('click',()=>{
 stopObservation(); pollingIndicator.textContent='Observation stopped · Worker continues';
});
document.querySelector('#resumeObserving').addEventListener('click',()=>startObservation(true));
document.querySelector('#downloadTrace').addEventListener('click',()=>{
 const blob=new Blob([JSON.stringify(observationLog,null,2)],{type:'application/json'});
 const url=URL.createObjectURL(blob);
 const link=document.createElement('a'); link.href=url; link.download='imagelab-request-log.json'; link.click(); URL.revokeObjectURL(url);
});

// Try again observes the existing status URL; it does not send another upload.
retryButton.addEventListener('click',()=>startObservation(true));
// When leaving the page, cancel observation and release the local preview URL.
window.addEventListener('pagehide',()=>{
	pageClosed=true;
	stopObservation();
	if(previewURL)URL.revokeObjectURL(previewURL);
});
// A page restored from the back/forward cache needs a fresh observation loop.
window.addEventListener('pageshow',event=>{
	pageClosed=false;
	if(event.persisted&&activeJob)startObservation(true);
});
