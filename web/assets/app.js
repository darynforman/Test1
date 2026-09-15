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

function choose(file){
	errorBox.textContent='';
	if(!file)return;
	if(!['image/jpeg','image/png'].includes(file.type)||file.size>10*1024*1024){
		clearSelection();
		fileInput.value='';
		errorBox.textContent='Choose a JPEG or PNG image no larger than 10 MB.';
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

dropzone.addEventListener('click',event=>{if(!event.target.closest('#replace'))fileInput.click();});
document.querySelector('#replace').addEventListener('click',event=>{event.stopPropagation();fileInput.click();});
fileInput.addEventListener('change',()=>choose(fileInput.files[0]));
['dragenter','dragover'].forEach(name=>dropzone.addEventListener(name,event=>{event.preventDefault();dropzone.classList.add('drag');}));
['dragleave','drop'].forEach(name=>dropzone.addEventListener(name,event=>{event.preventDefault();dropzone.classList.remove('drag');}));
dropzone.addEventListener('drop',event=>choose(event.dataTransfer.files[0]));

processButton.addEventListener('click',async()=>{
	if(isSubmitting||!chosenFile)return;
	// Lock immediately, before fetch, so rapid clicks cannot overlap requests.
	isSubmitting=true;
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
		if(response.status!==202)throw new Error(data.error||'Upload was rejected.');
		document.querySelector('#jobTitle').textContent='Upload accepted';
		document.querySelector('#jobText').textContent=`Job #${data.job_id}: ${data.status}. Status resource: ${data.status_url}`;
	}catch(error){
		errorBox.textContent=error.message;
	}finally{
		isSubmitting=false;
		processButton.disabled=!chosenFile;
		processButton.textContent='⇧  Process image';
	}
});
