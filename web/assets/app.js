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
		startObservation();
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
	step.querySelector('span').textContent={done:'Done',pending:'Pending',active:'In progress',failed:'Failed'}[state];
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
	if(!job||job.id!==previous.id||job.image_id!==previous.image_id||!['queued','processing','completed','failed'].includes(job.status))return false;
	for(const field of ['queued_at','started_at','completed_at','failed_at']){
		if(job[field]!=null&&(typeof job[field]!=='string'||!Number.isFinite(Date.parse(job[field]))))return false;
	}
	if(!job.queued_at||(job.status!=='queued'&&!job.started_at))return false;
	if(job.status==='failed'&&(!job.failed_at||(job.error!=null&&typeof job.error!=='string')))return false;
	if(job.status==='completed'){
		if(!job.completed_at||!Array.isArray(job.variants)||job.variants.length!==3)return false;
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
	observation.controller.abort();
	observation=null;
}

// Start one polling session for an active (queued or processing) job.
// Normally wait one second; immediate=true lets Try again check without waiting.
// Replacing the old session prevents multiple polling loops for the same page.
function startObservation(immediate=false){
	stopObservation();
	if(!activeJob||!['queued','processing'].includes(activeJob.status)||pageClosed)return;
	retrievalError.hidden=true;
	pollingIndicator.textContent='Checking status automatically · Every 1 second';
	const session={controller:new AbortController(),timer:null};
	observation=session;
	// Schedule after each response, so slow requests can never overlap.
	session.timer=setTimeout(()=>pollJob(session),immediate?0:1000);
}

// Fetch the current job state, allowing up to ten seconds for the request.
// After a valid response, redraw the job and schedule another check in one second
// if still active. Completed or failed ends the loop. Retrieval errors pause it
// and offer Try again while preserving the last known job state.
async function pollJob(session){
	if(observation!==session)return;
	let timedOut=false;
	const timeout=setTimeout(()=>{timedOut=true;session.controller.abort();},10000);
	try{
		const response=await fetch(activeJob.status_url,{signal:session.controller.signal,cache:'no-store'});
		if(!response.ok)throw new Error('Status retrieval failed');
		const job=await response.json();
		// Ignore stale responses even if cancellation raced with response parsing.
		if(observation!==session)return;
		if(timedOut||!validJob(job,activeJob))throw new Error('Unusable status response');
		activeJob={...job,status_url:activeJob.status_url};
		renderJob();
		if(job.status==='completed'||job.status==='failed'){
			stopObservation();
			pollingIndicator.textContent='Status checking stopped';
		}else{
			session.timer=setTimeout(()=>pollJob(session),1000);
		}
	}catch(error){
		if(observation!==session)return;
		stopObservation();
		pollingIndicator.textContent='Status checking paused';
		retrievalError.hidden=false;
	}finally{
		clearTimeout(timeout);
	}
}

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
