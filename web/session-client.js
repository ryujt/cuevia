(() => {
  'use strict';
  async function request(url, options={}) {
    const response=await fetch(url,{cache:'no-store',...options});
    const data=await response.json();
    if(!response.ok){const error=new Error(data.error||'request_failed');error.status=response.status;error.data=data;throw error;}
    return data;
  }
  const sessionURL=id=>'/api/session/'+encodeURIComponent(id);
  window.CueviaSessionClient={
    current:()=>request('/api/session/current'),
    get:id=>request(sessionURL(id)),
    runs:()=>request('/api/sessions'),
    startRun:(id,revision,mode,sceneId)=>request(sessionURL(id)+'/runs',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({revision,mode,sceneId:sceneId||null})}),
    save:(id,revision,session,draft)=>request(sessionURL(id),{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({revision,session,draft})}),
    activity:since=>request('/api/capture/activity?since='+since),
    activate:()=>request('/api/capture/select',{method:'POST'}),
    capture:id=>request('/api/capture/'+encodeURIComponent(id)),
    transcriptionStatus:()=>request('/api/transcription/status'),
    settings:()=>request('/api/settings'),
    saveSettings:(apiKey,model)=>request('/api/settings',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({apiKey,model})}),
    async uploadAudio(id,recordingId,result){
      const params=new URLSearchParams({recordingId,startMs:Math.floor(result.startMs),endMs:Math.max(Math.floor(result.startMs)+1,Math.ceil(result.endMs)),contexts:JSON.stringify(result.contexts),metadata:JSON.stringify({timing:result.timing,startRequestMs:result.startRequestMs,startUncertaintyMs:result.startUncertaintyMs,discontinuities:result.discontinuities})});
      return request(sessionURL(id)+'/audio?'+params,{method:'POST',headers:{'Content-Type':'audio/wav'},body:result.blob});
    },
    transcribe:(id,audioId)=>request(sessionURL(id)+'/audio/'+encodeURIComponent(audioId)+'/transcribe',{method:'POST'}),
    frame:(id,sceneId,revision,blob)=>request(sessionURL(id)+'/frame/'+encodeURIComponent(sceneId)+'?revision='+revision,{method:'PUT',headers:{'Content-Type':'image/png'},body:blob}),
    materials:(id,revision)=>request(sessionURL(id)+'/materials?revision='+revision,{method:'POST'}),
    originals:id=>request(sessionURL(id)+'/original-audio'),
    original:(id,recordingId,source)=>request(sessionURL(id)+'/original-audio?'+new URLSearchParams({recordingId,metadata:JSON.stringify({startMs:source.startMs,endMs:source.endMs,contexts:source.contexts,discontinuities:source.discontinuities})}),{method:'POST',headers:{'Content-Type':'audio/webm'},body:source.blob})
  };
})();
