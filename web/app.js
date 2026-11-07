(() => {
  'use strict';
  const M=window.CueviaModel, API=window.CueviaSessionClient;
  const isWindows=typeof navigator!=='undefined'&&/Windows|Win32|Win64/.test(navigator.userAgent||navigator.platform||'');
  const captureShortcut=isWindows?'Ctrl + Alt + S':'⌘ Command + ⌃ Control + S';
  const saveShortcut=isWindows?'Ctrl Enter로 저장':'⌘ Enter로 저장';
  const params=new URLSearchParams(location.search), editorId=crypto.randomUUID();
  const key='cuevia.real.v1',ownerKey='cuevia.editor.v1',leaseKey='cuevia.editor.lease.v1';
  const $=selector=>document.querySelector(selector);
  const escape=value=>String(value??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const clock=value=>{const seconds=Math.max(0,Math.floor(value));return `${String(Math.floor(seconds/60)).padStart(2,'0')}:${String(seconds%60).padStart(2,'0')}`;};
  const icons={logo:'M18 5a9 9 0 1 0 0 14M18 5v8h-8',plus:'M12 5v14M5 12h14',arrow:'M5 12h14m-5-5 5 5-5 5',close:'m6 6 12 12M6 18 18 6',mic:'M9 5a3 3 0 0 1 6 0v7a3 3 0 0 1-6 0zM5 10v2a7 7 0 0 0 14 0v-2M12 19v3',stop:'M6 6h12v12H6z',image:'M3 4h18v16H3zM3 16l6-6 5 5 3-3 4 4M15 8h.01',copy:'M8 8h13v13H8zM16 8V3H3v13h5',check:'m5 12 4 4L19 6',file:'M5 3h9l5 5v13H5zM14 3v6h5M9 13h6M9 17h4',edit:'m4 16 12-12 4 4L8 20H4zM13 7l4 4',trash:'M4 7h16M9 7V4h6v3M6 7l1 14h10l1-14M10 10v8M14 10v8',undo:'m9 5-6 6 6 6M3 11h11a6 6 0 0 1 6 6v3',point:'M12 3v4M12 17v4M3 12h4M17 12h4M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0',rect:'M4 4h16v16H4z',clear:'M4 7h16M9 7V4h6v3M6 7l1 14h10l1-14',lock:'M6 10h12v11H6zM8 10V6a4 4 0 0 1 8 0v4'};
  const icon=name=>`<svg viewBox="0 0 24 24" class="icon" aria-hidden="true"><path d="${icons[name]||icons.image}"/></svg>`;
  const button=(label,action,name,classes='',extra='')=>`<button class="button ${classes}" data-action="${action}" ${extra}>${name?icon(name):''}${label}</button>`;
  let restoreFocus=null,dialog=null;
  // The menu opens an independent settings tab. Return before session restore,
  // editor leases, recording initialization, timers, or local backups run.
  if(params.has('settings')){
    $('#app').innerHTML=`<main class="standby-screen"><div class="standby-brand">${icon('logo')}<b>cuevia.</b></div><div class="standby-center"><h1>Cuevia 설정</h1><p>OpenAI API 키와 전사 모델을 관리하세요.</p>${button('설정 열기','settings','lock','primary')}</div></main>`;
    document.addEventListener('click',event=>{const action=event.target.closest('[data-action]')?.dataset.action;if(action==='close'||event.target.matches('.backdrop'))closeDialog();else if(action==='settings')showSettings();});
    document.addEventListener('submit',event=>{if(event.target.id==='settings-form'){event.preventDefault();saveSettings();}});
    document.addEventListener('keydown',event=>{if(event.key==='Escape'){event.preventDefault();closeDialog();}if(event.key==='Tab'&&dialog)trapDialogFocus(event);});
    showSettings();return;
  }
  let session=M.createSession({title:'화면에 대한 요청',mode:'mixed',mic:true,project:'개인 프로젝트'}),revision=0;
  let sceneId=null,targetId=null,selectedSpeech=null,noteDraft='',draftContext=null,editingId=null;
  let waiting=true,inactive=false,booting=true,observer=null,annotationCleanup=null,tool='point',undoStack=[];
  let voiceState='idle',recorder=null,recordingId=null,recordingSessionId=null,recordingResult=null,voiceError='',audioBusy=false,selectionBusy=false;
  let saveState='saved',saveTimer=null,saveChain=Promise.resolve(),lastSavedHash='';
  let elapsedBase=0,clockAnchor=performance.now(),clockRunning=false,sourceImages=new Set(),polling=false;
  let unsavedAudio=null,transcriptionReady=false,completedRecordings=new Map(),recordingStarting=false,activeAudioStartMs=0,originalAudio=null;
  let runTransition=false;
  let activitySeq=0,activityPolling=false,recordingEpoch=0,materials=null,generating=false,originalSavePromise=null,lastPersistedAt=Date.now();
  const retentionMs=7*24*60*60*1000;
  try{const cached=JSON.parse(localStorage.getItem(key)||'null');if(cached&&(!Number.isFinite(cached.updatedAt)||Date.now()-cached.updatedAt>=retentionMs))localStorage.removeItem(key);}catch{}
  const audioNodes=new Map();
  const captureAcks=new Map(),pendingCaptures=[];
  const channel=typeof BroadcastChannel==='function'?new BroadcastChannel('cuevia-capture'):null;
  function nowMs(){return elapsedBase+(clockRunning?performance.now()-clockAnchor:0);}
  function syncClock(){session.elapsedMs=Math.round(nowMs());session.duration=session.elapsedMs/1000;}
  function setClock(ms,running){elapsedBase=Math.max(0,ms||0);clockAnchor=performance.now();clockRunning=running;}
  function visibleScenes(){return session.scenes.filter(scene=>!scene.deletedAt);}
  function visibleTranscript(){const deleted=new Set(session.scenes.filter(scene=>scene.deletedAt).map(scene=>scene.id));return session.transcript.filter(item=>!deleted.has(item.sceneId));}
  function currentScene(){const scenes=visibleScenes();return scenes.find(scene=>scene.id===sceneId)||scenes[0];}
  function sceneNumber(id){return visibleScenes().findIndex(scene=>scene.id===id)+1;}
  function supportedAnnotations(scene){return M.supportedAnnotations(scene);}
  function visibleMark(scene,id){return supportedAnnotations(scene).find(mark=>mark.id===id&&!mark.hidden);}
  function context(){const scene=currentScene();return {tMs:nowMs(),sceneId:selectionBusy?'unbound':scene?.id||'unbound',targetId:selectionBusy?null:visibleMark(scene,targetId)?.id||null};}

  function runActive(){return session.runState==='recording';}
  function markContext(){
    if(recorder?.state==='recording')recorder.markContext(context());
    if(runActive()&&session.runMode==='text'){
      const value=context();session.sceneSelections||=[];
      if(session.sceneSelections.at(-1)?.sceneId!==value.sceneId||session.sceneSelections.at(-1)?.targetId!==value.targetId){session.sceneSelections.push({tMs:Math.round(value.tMs),sceneId:value.sceneId,targetId:value.targetId});changed();}
    }
  }
  function descriptionContext(){
    const anchor=noteDraft.trim()&&draftContext?draftContext:{sceneId:currentScene()?.id,targetId};
    const scene=session.scenes.find(scene=>scene.id===anchor.sceneId),number=visibleMark(scene,anchor.targetId)?M.markNumber(scene,anchor.targetId):0;
    return sceneNumber(anchor.sceneId)>0?`화면 ${String(sceneNumber(anchor.sceneId)).padStart(2,'0')}${number?' · #'+number:''}`:'전체 메모';
  }
  function image(scene){return sourceImages.has(scene.captureId)?`/api/session/${encodeURIComponent(session.id)}/original/${scene.captureId}`:scene.imageUrl;}
  function draft(){return {sceneId,targetId,noteDraft,draftContext,editingId};}
  function saveLocal(){if(inactive)return;syncClock();try{localStorage.setItem(key,JSON.stringify({session,revision,draft:draft(),clockRunning,updatedAt:Date.now()}));}catch{}}
  function updateSaved(){const el=$('#save-status');if(el)el.innerHTML=saveState==='saving'?'저장 중…':saveState==='error'?'<button data-action="retry-save">저장 실패 · 다시 시도</button>':icon('check')+'기기에 저장됨';}
  function scheduleSave(){if(booting||inactive)return;saveLocal();clearTimeout(saveTimer);saveState='saving';updateSaved();saveTimer=setTimeout(()=>persist().catch(()=>{}),350);}
  function changed(){materials=null;session.prompt='';session.updatedAt=Date.now();scheduleSave();}
  function persist(){
    clearTimeout(saveTimer);saveLocal();
    const snapshot=structuredClone(session),snapshotDraft=structuredClone(draft());
    const fingerprint=JSON.stringify({session:snapshot,draft:snapshotDraft});
    const operation=saveChain.catch(()=>{}).then(async()=>{
      if(inactive||snapshot.id!==session.id)return null;
      if(fingerprint===lastSavedHash)return null;
      saveState='saving';updateSaved();
      try{const result=await API.save(snapshot.id,revision,snapshot,snapshotDraft);revision=result.revision;lastPersistedAt=Date.parse(result.updatedAt)||Date.now();lastSavedHash=fingerprint;sourceImages=new Set(snapshot.scenes.map(scene=>scene.captureId));saveState='saved';updateSaved();saveLocal();return result;}
      catch(error){saveState='error';updateSaved();if(error.message==='session_archived'){inactive=true;waiting=true;setClock(nowMs(),false);render();notify('이 버전은 아카이브됐어요. 최신 작업을 다시 열어주세요.');}else if(error.message==='revision_conflict')notify('다른 창에서 변경된 기록이 있어요. 다시 열어 최신 기록을 확인해주세요.');else notify('기록을 저장하지 못했어요. 입력은 이 창에 남아 있어요.');throw error;}
    });saveChain=operation;return operation;
  }
  function applySnapshot(snapshot){lastPersistedAt=Date.parse(snapshot.updatedAt)||Date.now();session=snapshot.session;revision=snapshot.revision;const saved=snapshot.draft||{};sceneId=saved.sceneId||session.scenes[0]?.id;targetId=saved.targetId||null;noteDraft=saved.noteDraft||'';draftContext=saved.draftContext||null;editingId=saved.editingId||null;session.recordings=snapshot.audio||[];sourceImages=new Set(session.scenes.map(scene=>scene.captureId));setClock(session.elapsedMs||session.duration*1000,false);
    try{const local=JSON.parse(localStorage.getItem(key)||'null');if(local?.session?.id===session.id){
      const elapsed=Math.max(session.elapsedMs||session.duration*1000||0,(local.session.elapsedMs||local.session.duration*1000||0));
      if(local.revision===revision){const audio=session.recordings;session=local.session;session.recordings=audio;const d=local.draft||{};sceneId=d.sceneId||sceneId;targetId=d.targetId||null;noteDraft=d.noteDraft||'';draftContext=d.draftContext||null;editingId=d.editingId||null;}
      setClock(elapsed,false);
    }}catch{}
    if(snapshot.archivedAt){inactive=true;waiting=true;setClock(nowMs(),false);return;}
    if(runActive()){
      session.runState='interrupted';session.runEndedAt=Date.now();setClock(nowMs(),false);
    }
  }
  function expireCurrentSession(){
    if(runActive()||runTransition)return false;
    if(Date.now()-lastPersistedAt<retentionMs||audioBusy||generating||recordingStarting||originalSavePromise||recorder&&['requesting','recording','stopping'].includes(recorder.state))return false;
    recordingEpoch++;recorder?.cancel().catch(()=>{});recorder=null;recordingResult=null;unsavedAudio=null;originalAudio=null;recordingId=null;recordingSessionId=null;voiceState='idle';voiceError='';completedRecordings.clear();for(const entry of audioNodes.values())entry.player.pause();audioNodes.clear();
    try{localStorage.removeItem(key);}catch{}session=M.createSession({title:'화면에 대한 요청',mode:'mixed',mic:true,project:'개인 프로젝트'});revision=0;
    sceneId=null;targetId=null;undoStack=[];noteDraft='';draftContext=null;editingId=null;materials=null;sourceImages=new Set();lastSavedHash='';lastPersistedAt=Date.now();
    waiting=true;setClock(0,false);closeDialog();render();return true;
  }
  function renewLease(){try{localStorage.setItem(leaseKey,JSON.stringify({id:editorId,sessionId:session.id,at:Date.now()}));}catch{}}
  function anotherEditorActive(id=session.id){try{const lease=JSON.parse(localStorage.getItem(leaseKey)||'null');return lease&&lease.id!==editorId&&lease.sessionId===id&&Date.now()-lease.at<65000;}catch{return false;}}
  function claimEditor(){inactive=false;try{localStorage.setItem(ownerKey,editorId);}catch{}renewLease();}
  function releaseLease(){try{const lease=JSON.parse(localStorage.getItem(leaseKey)||'null');if(lease?.id===editorId)localStorage.removeItem(leaseKey);}catch{}}
  function sourceText(scene){const source=scene?.source;if(!source)return '캡처한 화면';return source.kind==='program'?`${source.name}${source.title?' · '+source.title:''} · PID ${source.pid}`:`${source.name} · ${source.width} × ${source.height} · ${Math.round((source.scale||1)*100)}%`;}
  function render(){
    const sceneScroll=$('.scenes')?.scrollLeft||0;annotationCleanup?.();annotationCleanup=null;
    if(waiting||inactive){renderWaiting();return;}
    const scene=currentScene();
    $('#app').innerHTML=`<main class="app"><header class="header"><div class="brand"><span>${icon('logo')}</span>cuevia<span class="brand-dot">.</span></div><span class="header-caption">화면과 함께, 생각을 전하세요.</span><div class="header-actions">${button('설정','settings','lock','quiet')}${button('대기 모드','standby','close','quiet')}${button('프롬프트 만들기','generate','arrow','primary')}</div></header><section class="session-heading"><div><div class="eyebrow">캡처한 화면을 보고 설명하세요</div><h1>${escape(session.title)}</h1></div><span class="save-state" id="save-status"></span></section><div class="workspace"><section class="canvas-panel" aria-label="설명할 화면"><div class="panel-heading"><span>${icon('image')}${escape(scene?.title||'설명할 화면')}</span>${button('화면 추가','capture','plus','small quiet')}</div><div class="stage" id="stage">${scene?`<div class="capture" id="capture"><div class="capture-title"><span class="window-dots"><i></i><i></i><i></i></span><span title="${escape(sourceText(scene))}">${escape(sourceText(scene))}</span><span></span></div><div class="capture-body" id="capture-body"><img src="${escape(image(scene))}" alt="${escape(scene.title)} 캡처 화면" draggable="false"></div></div>`:`<div class="empty"><div class="empty-icon">${icon('image')}</div><h2>설명할 화면을 골라주세요</h2><p>${isWindows?'트레이 메뉴 또는 Ctrl+Alt+S로 영역을 선택하세요.':'모니터나 프로그램을 선택해 캡처하세요.'}</p>${button('캡처하기','capture','image','primary')}</div>`}</div>${markToolbar(scene)}<div class="scenes" aria-label="캡처 이미지 목록">${visibleScenes().map((sc,index)=>`<div class="scene-item"><button class="scene ${sc.id===scene?.id?'selected':''}" data-action="scene" data-id="${escape(sc.id)}" aria-pressed="${sc.id===scene?.id}"><img src="${escape(image(sc))}" alt=""><span><b>${String(index+1).padStart(2,'0')}</b>${escape(sc.title)}</span></button><button class="scene-delete" data-action="delete-scene" data-id="${escape(sc.id)}" aria-label="${escape(sc.title)} 캡처 삭제" title="캡처 삭제">${icon('trash')}</button></div>`).join('')}${session.scenes.some(sc=>sc.deletedAt)?button('삭제 취소','restore-scene','undo','small quiet'):''}<button class="add-scene" data-action="capture" aria-label="다른 화면 추가">${icon('plus')}</button></div></section><aside class="description-panel"><div class="panel-heading"><span>설명</span><span class="context-count">${visibleTranscript().length}개 기록</span></div><div class="voice-control" id="voice-control"></div><div class="recording-list" id="recording-list"></div><div class="descriptions" id="descriptions"></div><form class="composer" id="composer"><label for="description-text">${editingId?'설명 수정':'텍스트로 설명하기'}</label><div class="composer-context">${escape(descriptionContext())}</div><textarea id="description-text" placeholder="예: 이 버튼을 오른쪽 위로 옮겨주세요." aria-label="텍스트 설명" ${runActive()||editingId?'':'disabled'}>${escape(noteDraft)}</textarea><div class="composer-footer"><span>${runActive()||editingId?saveShortcut:'녹음 또는 기록을 시작하세요'}</span>${editingId?button('취소','cancel-edit',null,'small quiet'):''}<button class="button small primary" type="submit" ${runActive()||editingId?'':'disabled'}>${icon('plus')}${editingId?'수정 저장':'설명 남기기'}</button></div></form></aside></div><footer class="footer">${icon('lock')} 화면·음성은 로컬에 저장하고 음성은 OpenAI에서 전사해요<span>${captureShortcut}</span></footer></main>`;
    renderTranscript();updateVoice();renderRecordings();updateSaved();bindAnnotation();
    observer?.disconnect();if($('#stage')){observer=new ResizeObserver(fitCapture);observer.observe($('#stage'));}fitCapture();
    if($('.scenes'))$('.scenes').scrollLeft=sceneScroll;
  }
  function markToolbar(scene){
    return `<div class="mark-tools" role="toolbar" aria-label="화면 표시 도구">${[['point','point','순번'],['arrow','arrow','화살표'],['rect','rect','네모']].map(([value,name,label])=>`<button class="mark-tool ${tool===value?'selected':''}" data-action="tool" data-tool="${value}" aria-pressed="${tool===value}" ${scene?'':'disabled'}>${icon(name)}${label}</button>`).join('')}<span class="mark-tool-separator"></span><button class="mark-tool" data-action="undo-mark" ${undoStack.some(item=>item.sceneId===scene?.id)?'':'disabled'}>${icon('undo')}뒤로</button><button class="mark-tool" data-action="clear-marks" ${supportedAnnotations(scene).some(mark=>!mark.hidden)?'':'disabled'}>${icon('clear')}전체 지우기</button></div>`;
  }
  function bindAnnotation(){
    const scene=currentScene(),element=$('#capture-body');if(!scene||!element)return;
    annotationCleanup=window.CueviaAnnotation.mount({element,scene,tool,selectedId:targetId,beforeDraw:()=>!inactive&&!generating&&!runTransition,
      onCommit(mark){
        if(!['point','arrow','rect'].includes(mark.type))return;
        if(noteDraft.trim()&&!draftContext)draftContext=context();
        scene.annotations||=[];scene.annotations.push({...mark,createdAtMs:Math.round(nowMs())});
        undoStack.push({kind:'create',sceneId:scene.id,ids:[mark.id]});targetId=mark.id;markContext();changed();render();
      },
      onSelect(id){if(noteDraft.trim()&&!draftContext)draftContext=context();targetId=visibleMark(scene,id)?.id||null;markContext();scheduleSave();render();}
    });
  }
  function clearMarks(){
    const scene=currentScene(),marks=supportedAnnotations(scene).filter(mark=>!mark.hidden);if(!marks.length)return;
    if(noteDraft.trim()&&!draftContext)draftContext=context();
    undoStack.push({kind:'clear',sceneId:scene.id,ids:marks.map(mark=>mark.id)});
    marks.forEach(mark=>{mark.hidden=true;});targetId=null;markContext();changed();render();
  }
  function undoMark(){
    const scene=currentScene(),index=undoStack.findLastIndex(item=>item.sceneId===scene?.id);if(index<0)return;
    if(noteDraft.trim()&&!draftContext)draftContext=context();
    const action=undoStack.splice(index,1)[0];
    supportedAnnotations(scene).filter(mark=>action.ids.includes(mark.id)).forEach(mark=>{mark.hidden=action.kind==='create';});
    targetId=null;markContext();changed();render();
  }
  function screenUncertain(item){
    if(item.timingUncertain)return true;
    const scenes=new Set((item.candidates||[]).map(candidate=>candidate.sceneId));
    return scenes.size?scenes.size!==1||!scenes.has(item.sceneId)||scenes.has('unbound')||scenes.has(null):!!item.unresolved;
  }
  function renderTranscript(){const pane=$('#descriptions');if(!pane)return;pane.innerHTML=visibleTranscript().length?visibleTranscript().map(item=>`<article class="description ${item.id===selectedSpeech?'selected':''}"><div class="description-meta"><button data-action="speech" data-id="${escape(item.id)}">${item.timingEstimated?'녹음 '+clock(item.startMs/1000)+'–'+clock(item.endMs/1000):clock(item.t)}</button>${item.timingEstimated?'<span>문장별 시각 없음 · 녹음 전체 구간</span>':''}<span>${screenUncertain(item)?'화면 연결 확인':(sceneNumber(item.sceneId)>0?`화면 ${String(sceneNumber(item.sceneId)).padStart(2,'0')}`:'전체 메모')}</span><button class="edit" data-action="edit" data-id="${escape(item.id)}" aria-label="설명 수정">${icon('edit')}</button></div><button class="description-text" data-action="speech" data-id="${escape(item.id)}">${escape(item.text)}</button></article>`).join(''):'<div class="empty-description"><p>말하거나, 아래에 적어주세요.</p><small>선택한 화면에 설명이 함께 남아요.</small></div>';const count=$('.context-count');if(count)count.textContent=visibleTranscript().length+'개 기록';}
  function updateVoice(){
    const el=$('#voice-control');if(!el)return;
    const busy=selectionBusy||runTransition||recordingStarting||audioBusy||!!originalSavePromise||['requesting','stopping','saving','transcribing'].includes(voiceState);
    const active=runActive(),mode=session.runMode;
    const title=voiceError|| (active?(mode==='text'?'텍스트 기록 중':'마이크 녹음 중'):session.runState==='interrupted'?'이전 작업이 중단됐어요':'새 녹음 또는 기록을 시작하세요');
    const hint=active?(mode==='text'?'화면 선택과 텍스트 설명을 시간순으로 남겨요.':'음성은 OpenAI로 전사해요.'):session.runVersion?`v${session.runVersion} · 새로 시작하면 이 버전은 아카이브돼요.`:'이전 작업은 별도 버전으로 보관해요.';
    el.innerHTML=`<div class="run-toggles" role="group" aria-label="기록 방식"><button class="button ${active&&mode==='audio'?'record-stop':'quiet'}" data-action="toggle-audio" aria-pressed="${active&&mode==='audio'}" ${busy?'disabled':''}>${icon('mic')}녹음</button><button class="button ${active&&mode==='text'?'record-stop':'quiet'}" data-action="toggle-text" aria-pressed="${active&&mode==='text'}" ${busy?'disabled':''}>${icon('file')}기록</button>${button('아카이브','history','file','small quiet',busy?'disabled':'')}</div><div class="voice-status"><span><b>${escape(title)}</b><small>${active?`v${session.runVersion} · <span id="record-time">${clock(nowMs()/1000)}</span> · `:''}${escape(hint)}</small></span></div>${unsavedAudio?button('오디오 다시 저장','retry-audio',null,'small quiet',busy?'disabled':''):''}${originalAudio?button('원음 저장','retry-original',null,'small quiet',busy?'disabled':''):''}`;
  }
  function renderRecordings(){
    const el=$('#recording-list');if(!el)return;
    for(const audio of [...(session.recordings||[]),...(session.originalRecordings||[])]){
      let entry=audioNodes.get(audio.id);
      if(!entry){
        const node=document.createElement('div');node.className='recorded-audio';
        node.innerHTML='<div class="audio-meta"><span></span><span></span></div><audio controls preload="none"></audio><div class="audio-actions"></div>';
        const player=node.querySelector('audio');player.src=audio.url;player.setAttribute('aria-label','저장된 녹음 재생');
        entry={node,player};audioNodes.set(audio.id,entry);
      }
      const labels=entry.node.querySelectorAll('.audio-meta span');
      const start=audio.startMs??audio.metadata?.startMs,end=audio.endMs??audio.metadata?.endMs;
      labels[0].textContent=Number.isFinite(start)&&Number.isFinite(end)?`녹음 ${clock(start/1000)}–${clock(end/1000)}`:'변환 전 원음';
      labels[1].textContent=({complete:'전사 완료',pending:'전사 대기',failed:'전사 실패',unconverted:'원음 보관됨'})[audio.transcriptionStatus]||'전사 대기';
      entry.node.querySelector('.audio-actions').innerHTML=!['complete','unconverted'].includes(audio.transcriptionStatus)?button('전사 다시 시도','transcribe','mic','small quiet',`data-id="${escape(audio.id)}" ${audioBusy?'disabled':''}`):'';
      if(entry.node.parentNode!==el)el.append(entry.node);
    }
  }
  function fitCapture(){const stage=$('#stage'),capture=$('#capture'),scene=currentScene();if(!stage||!capture||!scene)return;const ratio=(scene.width||1200)/(scene.height||760);capture.style.width=Math.max(1,Math.min(stage.clientWidth-40,(stage.clientHeight-56)*ratio))+'px';}
  function renderWaiting(){observer?.disconnect();$('#app').innerHTML=`<main class="standby-screen"><header class="standby-header"><div class="standby-brand">${icon('logo')}<b>cuevia.</b></div>${button('설정','settings','lock','quiet')}</header><div class="standby-center"><div class="standby-icon">${icon('image')}</div><h1>${inactive?'다른 창으로 기록을 이어갔어요':'캡처할 때 바로 만나요'}</h1><p>${isWindows?'Ctrl+Alt+S를 누르고 캡처할 영역을 드래그하세요.':'단축키를 누르고 모니터나 프로그램을 선택하세요.'}<br>캡처한 화면을 보며 말하거나 글로 설명할 수 있어요.</p><div class="shortcut">${isWindows?'<kbd>Ctrl</kbd><kbd>Alt</kbd><kbd>S</kbd>':'<kbd>⌘ Command</kbd><kbd>⌃ Control</kbd><kbd>S</kbd>'}</div>${button(booting?'기록을 불러오는 중…':'캡처하기','capture','image','primary',booting?'disabled':'')}<div class="run-toggles standby-runs">${button('녹음','toggle-audio','mic','quiet',booting?'disabled':'')}${button('기록','toggle-text','file','quiet',booting?'disabled':'')}${button('아카이브','history','file','quiet',booting?'disabled':'')}</div><small>창 없이 백그라운드에서 기다려요.</small>${session.scenes.length?button('최근 기록 열기','resume-session',null,'quiet'):''}</div><div class="standby-footer">${isWindows?'선택한 화면 영역이 이미지로 기록돼요.':'모니터 정보 또는 프로그램의 PID가 요청에 함께 기록돼요.'}</div></main>`;}
  async function activateCapture(){if(noteDraft.trim())return notify('입력 중인 설명을 먼저 저장해주세요.');if(booting)return;selectionBusy=true;markContext();channel?.postMessage({type:'selecting',sessionId:session.id,value:true});try{await API.activate();}catch(error){selectionBusy=false;markContext();notify(error.message==='busy'?'이미 캡처 대상을 선택하고 있어요.':'캡처 서비스를 시작하지 못했어요. Cuevia가 실행 중인지 확인해주세요.');}}
  function notify(message){clearTimeout(notify.timer);$('#toast').innerHTML=`<div class="toast-message">${icon('check')}${escape(message)}</div>`;notify.timer=setTimeout(()=>$('#toast').innerHTML='',3800);}
  function showDialog(title,description,body,footer){restoreFocus=document.activeElement;dialog='prompt';$('#modal-root').innerHTML=`<div class="backdrop"><section class="dialog prompt-dialog" role="dialog" aria-modal="true" aria-labelledby="dialog-title"><div class="dialog-heading"><div><h2 id="dialog-title">${escape(title)}</h2><p>${escape(description)}</p></div><button class="close" data-action="close" aria-label="닫기">${icon('close')}</button></div><div class="prompt-content">${body}</div><div class="dialog-footer">${footer}</div></section></div>`;setTimeout(()=>$('#modal-root button')?.focus(),20);}
  function closeDialog(){dialog=null;$('#modal-root').innerHTML='';if(restoreFocus?.isConnected)restoreFocus.focus();}
  function trapDialogFocus(event){const elements=[...document.querySelectorAll('.dialog button:not(:disabled),.dialog input:not(:disabled),.dialog select:not(:disabled)')],first=elements[0],last=elements.at(-1);if(event.shiftKey&&document.activeElement===first){event.preventDefault();last?.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus();}}
  function settingsModelHint(model){return model==='whisper-1'?'기본 모델 · 발화 구간별 시각으로 화면과 연결해요.':'문장별 시각 없이 녹음 전체를 하나의 구간으로 전사해요. 화면 연결을 직접 확인해야 해요.';}
  async function showSettings(){
    showDialog('설정','OpenAI 음성 전사', '<p>설정을 불러오는 중…</p>','');dialog='settings';
    try{
      const settings=await API.settings();if(dialog!=='settings')return;
      const labels={'whisper-1':'whisper-1 · 기본 · 구간 시각 지원','gpt-4o-transcribe':'gpt-4o-transcribe · 구간 시각 없음','gpt-4o-mini-transcribe':'gpt-4o-mini-transcribe · 구간 시각 없음'};
      const selectedModel=settings.models.includes(settings.model)?settings.model:'whisper-1';
      const options=settings.models.filter(model=>labels[model]).map(model=>`<option value="${escape(model)}" ${selectedModel===model?'selected':''}>${labels[model]}</option>`).join('');
      const body=`<form id="settings-form" class="settings-form"><label for="openai-key">OpenAI API 키</label><input id="openai-key" name="apiKey" type="password" autocomplete="new-password" spellcheck="false" placeholder="${settings.apiKeyConfigured?'새 키를 입력하면 교체돼요':'OpenAI API 키 입력'}"><p id="settings-key-state">${settings.apiKeyConfigured?'키가 저장되어 있어요. 비워 두면 기존 키를 유지해요.':'저장된 API 키가 없어요.'}</p><label for="transcription-model">전사 모델</label><select id="transcription-model" name="model">${options}</select><p id="settings-model-hint">${settingsModelHint(selectedModel)}</p><div class="settings-policy">저장 후 다음 전사 요청부터 적용돼요. 진행 중인 전사는 기존 설정으로 완료하고 녹음은 계속할 수 있어요.</div><p class="settings-location">설정 파일: <code>${escape(settings.storagePath)}</code></p><p id="settings-message" role="status" aria-live="polite"></p></form>`;
      showDialog('설정','OpenAI 음성 전사',body,`<button type="submit" form="settings-form" id="settings-save" class="button primary">저장</button>`);dialog='settings';
      $('#transcription-model')?.addEventListener('change',event=>{$('#settings-model-hint').textContent=settingsModelHint(event.target.value);});
    }catch{if(dialog==='settings'){$('.prompt-content').innerHTML='<p>설정을 불러오지 못했어요. Cuevia가 실행 중인지 확인해주세요.</p>';}}
  }
  async function saveSettings(){
    const field=$('#openai-key'),select=$('#transcription-model'),save=$('#settings-save'),message=$('#settings-message');if(!field||!select||!save||save.disabled)return;
    const key=field.value;field.value='';save.disabled=true;message.textContent='저장 중…';
    try{
      const settings=await API.saveSettings(key,select.value);if(dialog!=='settings')return;
      field.value='';field.placeholder='새 키를 입력하면 교체돼요';$('#settings-key-state').textContent=settings.apiKeyConfigured?'키가 저장되어 있어요. 비워 두면 기존 키를 유지해요.':'저장된 API 키가 없어요.';
      message.textContent='저장했어요. 다음 전사 요청부터 적용돼요.';
    }catch(error){if(dialog==='settings')message.textContent=({invalid_model:'지원되는 전사 모델을 선택해주세요.',invalid_api_key:'API 키에 공백이나 줄바꿈이 없는지 확인해주세요.',origin_rejected:'Cuevia의 로컬 설정 화면을 다시 열어주세요.'})[error.message]||'설정을 저장하지 못했어요. 키를 입력했다면 다시 입력해주세요.';}
    finally{field.value='';save.disabled=false;}
  }
  function markdown(text){return text.split('\n').map(line=>line.startsWith('# ')?`<h3>${escape(line.slice(2))}</h3>`:line.startsWith('## ')?`<h4>${escape(line.slice(3))}</h4>`:line?`<p>${escape(line)}</p>`:'').join('');}
  function promptDialog(){
    const insufficient=materials.prompt.includes('입력 부족:');
    const path=`<div class="local-path"><span>로컬 자료</span><code>${escape(materials.directory)}</code></div>`;
    showDialog(insufficient?'설명을 먼저 확인해주세요':'에이전트에게 전달할 프롬프트',insufficient?'저장한 원음과 설명을 확인해주세요.':'필요한 이미지와 기록의 절대 경로가 포함돼요.',path+markdown(materials.prompt),`<small>자료는 기기에 저장되며 7일이 지나면 자동 삭제돼요.</small>${button('프롬프트 복사','copy','copy','primary')}`);
  }
  async function prepareMaterials(){
    if(noteDraft.trim())submitDescription(true);
    syncClock();setClock(session.elapsedMs,false);
    const wasActive=runActive();if(wasActive){session.runState='stopped';session.runEndedAt=Date.now();}
    await stopRecording();if(recordingResult)await recordingResult;
    if(originalAudio)await saveOriginalAudio();if(originalSavePromise)await originalSavePromise;
    if(unsavedAudio)throw Error('unsaved_audio');
    syncClock();setClock(session.elapsedMs,false);
    await refreshAudio();
    if(!visibleScenes().length&&!visibleTranscript().length&&!session.notes?.trim()&&!(session.recordings||[]).length&&!(session.originalRecordings||[]).length){session.prompt='';await persist();materials=null;return null;}
    session.promptSceneIds=M.promptScenes(session).map(scene=>scene.id);
    session.prompt=M.generatePrompt(session);session.status='review';await persist();
    const savedRevision=revision,sessionID=session.id,copy=structuredClone(session);
    for(const scene of M.promptScenes(copy)){
      if(!supportedAnnotations(scene).some(mark=>!mark.hidden))continue;
      const frame=await window.CueviaFrame.render({...scene,imageUrl:image(scene)});
      if(inactive||revision!==savedRevision||session.id!==sessionID)throw Error('changed');
      await API.frame(sessionID,scene.id,savedRevision,frame);
    }
    const result=await API.materials(sessionID,savedRevision);
    if(inactive||revision!==savedRevision||session.id!==sessionID)throw Error('changed');
    materials=result;return result;
  }
  async function generate(){
    if(selectionBusy||generating||runTransition)return;
    generating=true;
    if($('.workspace'))$('.workspace').inert=true;
    try{if(await prepareMaterials())promptDialog();else notify('캡처나 설명을 남긴 뒤 프롬프트를 만들어주세요.');}
    catch(error){notify(error.message==='changed'?'기록이 바뀌었어요. 다시 만들어주세요.':'자료를 저장하지 못했어요. 기록은 유지돼요. 다시 시도해주세요.');}
    finally{generating=false;render();drainCaptures();}
  }
  async function endRun(){
    runTransition=true;updateVoice();if($('.workspace'))$('.workspace').inert=true;
    try{await prepareMaterials();notify('이 버전의 기록과 프롬프트를 저장했어요.');}
    finally{runTransition=false;render();drainCaptures();}
  }
  async function toggleRun(mode){
    if(booting||selectionBusy||runTransition||generating||recordingStarting||audioBusy||originalSavePromise)return;
    if(anotherEditorActive())return notify('다른 창에서 이 버전을 기록하고 있어요. 기존 창에서 종료한 뒤 시작해주세요.');
    if(runActive()&&session.runMode===mode)return endRun();
    const priorID=session.id;claimEditor();
    runTransition=true;updateVoice();if($('.workspace'))$('.workspace').inert=true;
    try{
      if(noteDraft.trim())submitDescription(true);
      const hasWork=session.transcript.length||session.notes?.trim()||(session.recordings||[]).length||(session.originalRecordings||[]).length||session.scenes.some(scene=>supportedAnnotations(scene).some(mark=>!mark.hidden))||runActive()||session.runState==='stopped'||session.runState==='interrupted';
      if(hasWork)await prepareMaterials();else await persist();
      clearTimeout(saveTimer);await saveChain;
      const next=await API.startRun(session.id,revision,mode,currentScene()?.id||null);
      await activateRunSnapshot(next,mode);
    }catch(error){
      let recovered=false;
      if(anotherEditorActive()){inactive=true;waiting=true;render();return;}
      try{const latest=await API.current();if((latest.session.previousRunId===priorID||(latest.session.id===priorID&&latest.revision>revision))&&latest.session.runMode===mode&&latest.session.runState==='ready'&&!anotherEditorActive(latest.session.id)){await activateRunSnapshot(latest,mode);recovered=true;}}catch{}
      if(!recovered)notify(error.message==='session_archived'?'이미 아카이브된 버전이에요. 최신 작업을 다시 열어주세요.':'새 버전을 시작하지 못했어요. 기존 기록은 유지돼요. 저장 상태를 확인해주세요.');
    }
    finally{runTransition=false;render();drainCaptures();}
  }
  async function activateRunSnapshot(next,mode){
      for(const entry of audioNodes.values())entry.player.pause();audioNodes.clear();
      recorder=null;recordingResult=null;completedRecordings.clear();voiceState='idle';voiceError='';materials=null;lastSavedHash='';
      noteDraft='';draftContext=null;editingId=null;selectedSpeech=null;sceneId=null;targetId=null;undoStack=[];
      applySnapshot(next);claimEditor();waiting=false;setClock(0,false);
      if(mode==='text'){
        session.runState='recording';session.runStartedAt=Date.now();setClock(0,true);markContext();changed();await persist();
      }else await startRecording();
      render();
  }
  async function showHistory(){
    const runs=await API.runs(),archived=runs.filter(run=>run.archivedAt);
    const body=archived.length?archived.map(run=>`<button class="archive-row" data-action="view-archive" data-id="${escape(run.id)}"><b>${run.runVersion?'v'+run.runVersion:'이전 기록'} · ${run.runMode==='text'?'기록':run.runMode==='audio'?'녹음':escape(run.title)}</b><span>${escape(new Date(run.archivedAt).toLocaleString())} · 설명 ${run.transcriptCount}개 · 오디오 ${run.audioCount}개</span></button>`).join(''):'<p>새 녹음이나 기록을 시작하면 이전 버전이 여기에 보관돼요.</p>';
    showDialog('아카이브','이전 버전의 프롬프트와 오디오를 확인하세요.',body,button('닫기','close',null,'quiet'));
  }
  async function viewArchive(id){
    const [snapshot,originals]=await Promise.all([API.get(id),API.originals(id)]);
    if(!snapshot.archivedAt)throw Error('not_archived');
    let prompt=snapshot.session.prompt||'저장된 프롬프트가 없어요.',path='';
    if(snapshot.session.prompt){try{const saved=await API.materials(id,snapshot.revision);prompt=saved.prompt;path=`<div class="local-path"><span>로컬 자료</span><code>${escape(saved.directory)}</code></div>`;}catch{}}
    const audio=[...(snapshot.audio||[]),...(Array.isArray(originals)?originals:originals.originals||[])];
    const players=audio.map((item,index)=>`<div class="archived-audio"><label>오디오 ${index+1}</label><audio controls preload="none" src="${escape(item.url)}"></audio></div>`).join('');
    showDialog(`${snapshot.session.runVersion?'v'+snapshot.session.runVersion:'이전 기록'} · ${snapshot.session.runMode==='text'?'기록':'녹음'}`,'아카이브된 버전은 읽기 전용이에요.',path+players+markdown(prompt),button('목록','history',null,'quiet')+button('닫기','close',null,'quiet'));
  }
  function submitDescription(force=false){if(!force&&(inactive||runTransition||generating))return;const text=noteDraft.trim();if(!text)return $('#description-text')?.focus();if(!force&&!runActive()&&!editingId)return notify('녹음 또는 기록을 먼저 시작해주세요.');if(editingId){const item=session.transcript.find(item=>item.id===editingId);if(item){item.text=text;item.updatedAt=Date.now();}}else{const c=draftContext||context();session.transcript.push({id:'text-'+crypto.randomUUID(),kind:'note',text,t:c.tMs/1000,sceneId:c.sceneId==='unbound'?null:c.sceneId,target:c.targetId||null,updatedAt:Date.now()});}noteDraft='';editingId=null;draftContext=null;changed();render();$('#description-text')?.focus();}
  function transcriptionError(error){return ({unauthorized:'OpenAI API 키와 이용 권한을 확인해주세요. 원음은 로컬에 보관돼요.',rate_limited:'OpenAI 사용 한도를 확인한 뒤 다시 시도해주세요. 원음은 보관돼요.',unavailable:'OpenAI 연결과 설정의 API 키를 확인해주세요. 저장한 설정은 바로 적용돼요.',timeout:'전사 응답이 늦어지고 있어요. 원음은 보관돼 있으니 다시 시도할 수 있어요.',busy:'다른 음성을 전사하고 있어요. 완료된 뒤 다시 시도해주세요.'})[error.message]||'원음은 저장됐어요. 전사를 완료하지 못했으니 다시 시도해주세요.';}
  function recordingError(error){return ({recording_unsupported:'이 Chrome 환경에서 마이크 녹음을 지원하지 않아요.',recording_format_unsupported:'이 Chrome에서 녹음 형식을 지원하지 않아요.',microphone_disconnected:'마이크 연결이 끊겼어요. 연결을 확인한 뒤 다시 시작해주세요.',microphone_denied:'마이크 사용이 허용되지 않았어요.',microphone_missing:'사용할 수 있는 마이크가 없어요.',microphone_unavailable:'마이크를 사용할 수 없어요.',audio_decode_failed:'원음 형식을 변환하지 못했어요. 원음을 따로 보관할 수 있어요.'})[error.code]||'녹음을 마치지 못했어요. 원음과 저장 상태를 확인해주세요.';}
  function retainOriginal(error){
    if(error.originalBlob?.size&&!originalAudio){
      originalAudio={blob:error.originalBlob,startMs:error.startMs,endMs:error.endMs,contexts:error.contexts,discontinuities:error.discontinuities,id:recordingId,sessionID:recordingSessionId};
      saveOriginalAudio().catch(()=>{});
    }
  }
  function saveOriginalAudio(){
    if(originalSavePromise)return originalSavePromise;
    if(!originalAudio)return Promise.resolve();
    const source=originalAudio;
    originalSavePromise=(async()=>{
      try{
        const result=await API.original(source.sessionID,source.id,source);
        if(session.id===source.sessionID){session.originalRecordings||=[];if(!session.originalRecordings.some(item=>item.id===result.id))session.originalRecordings.push(result);}
        if(originalAudio===source)originalAudio=null;
        voiceError='원음은 로컬에 저장됐어요. 음성 변환을 완료하지 못했어요.';renderRecordings();
      }catch(error){voiceError='원음을 저장하지 못했어요. 이 창에서 원음 저장을 다시 눌러주세요.';throw error;}
      finally{originalSavePromise=null;updateVoice();}
    })();originalSavePromise.catch(()=>{});updateVoice();return originalSavePromise;
  }
  async function startRecording(){
    if(recordingStarting||audioBusy||unsavedAudio||originalAudio||recorder&&['recording','requesting','stopping'].includes(recorder.state))return;
    recordingStarting=true;voiceState='requesting';voiceError='';updateVoice();
    const epoch=++recordingEpoch,id='recording-'+crypto.randomUUID(),sessionID=session.id;
    recorder=null;
    recordingId=id;recordingSessionId=sessionID;recordingResult=null;
    try{
      await persist();
      if(inactive||waiting||epoch!==recordingEpoch){voiceState='idle';return;}
      recorder=window.CueviaRecorder.create({
        onState(state){if(['ready','idle'].includes(state))return;voiceState=state;updateVoice();},
        onError(error){syncClock();setClock(session.elapsedMs,false);if(runActive()){session.runState='interrupted';session.runEndedAt=Date.now();changed();}console.error('Cuevia microphone error',error.code||error.name,error);retainOriginal(error);voiceState='error';voiceError=recordingError(error);updateVoice();},
        onComplete(result){recordingResult=completeRecording(sessionID,id,result);}
      });
      const ownRecorder=recorder,started=await ownRecorder.start({getTimeMs:nowMs,getContext:context});
      if(inactive||waiting||epoch!==recordingEpoch){await ownRecorder.stop();return;}
      activeAudioStartMs=started.startMs;
      voiceState='recording';session.runState='recording';session.runStartedAt=Date.now();setClock(started.startMs||0,true);changed();await persist();
    }catch(error){
      console.error('Cuevia recording start failed',error.code||error.name,error);retainOriginal(error);
      if(recorder?.state==='recording'&&!inactive){voiceState='recording';voiceError='마이크 녹음 중 · 저장에 실패했어요. 종료 후 다시 저장해주세요.';notify(voiceError);}
      else {if(recorder?.state==='recording')await recorder.cancel().catch(()=>{});voiceState=error.code==='recording_canceled'?'idle':'error';voiceError=error.code==='recording_canceled'?'':recordingError(error);}
    }
    finally{recordingStarting=false;if(voiceState!=='recording'){session.runState='ready';setClock(0,false);await persist().catch(()=>{});}updateVoice();}
  }
  async function stopRecording(){
    recordingEpoch++;
    if(!recorder||recorder.state==='error')return;
    try{const result=await recorder.stop();if(result)recordingResult=completeRecording(recordingSessionId,recordingId,result);if(recordingResult)await recordingResult;}
    catch(error){retainOriginal(error);updateVoice();throw error;}
  }
  async function retryAudio(){
    if(audioBusy||!unsavedAudio)return;
    const pending=unsavedAudio;completedRecordings.delete(pending.id);
    recordingResult=completeRecording(pending.sessionID,pending.id,pending.result);
    await recordingResult;
  }
  function completeRecording(sessionID,id,result){
    if(completedRecordings.has(id))return completedRecordings.get(id);
    const operation=(async()=>{
      audioBusy=true;voiceState='saving';updateVoice();
      try{
        if(!inactive)await persist();
        const audio=await API.uploadAudio(sessionID,id,result);
        if(unsavedAudio?.id===id)unsavedAudio=null;
        channel?.postMessage({type:'audio',sessionId:sessionID});
        if(sessionID===session.id){upsertAudio(audio);renderRecordings();}
        voiceState='transcribing';updateVoice();
        try{
          const response=await API.transcribe(sessionID,audio.id);
          if(sessionID===session.id){upsertAudio(response.audio);mergeAudio(response.audio);}
          channel?.postMessage({type:'audio',sessionId:sessionID});voiceState='idle';voiceError='';
        }catch(error){voiceState='error';voiceError=transcriptionError(error);}
        if(!inactive){await refreshAudio();renderTranscript();renderRecordings();await persist();}
      }catch(error){
        // A successful upload already has a durable copy, even if later session
        // saving fails. Retrying the same recording ID remains idempotent.
        unsavedAudio={result,id,sessionID};voiceState='error';voiceError='녹음 처리를 마치지 못했어요. 이 창에서 다시 저장해주세요.';
        notify(voiceError);throw error;
      }finally{audioBusy=false;updateVoice();renderRecordings();}
    })();
    completedRecordings.set(id,operation);operation.catch(()=>{});return operation;
  }
  function upsertAudio(audio){session.recordings||=[];const index=session.recordings.findIndex(item=>item.id===audio.id);if(index<0)session.recordings.push(audio);else session.recordings[index]=audio;}
  function mergeAudio(audio){
    if(audio.transcriptionStatus!=='complete'||!Array.isArray(audio.transcription))return false;
    let added=false;const contexts=audio.contexts||[];
    audio.transcription.forEach((segment,index)=>{
      const id='speech-'+audio.id+'-'+index;if(session.transcript.some(item=>item.id===id))return;
      const start=audio.startMs+segment.startMs,end=audio.startMs+segment.endMs;
      const candidates=contexts.filter((c,i)=>{
        const from=c.sampleOffset??Math.round((c.tMs-audio.startMs)*16),next=contexts[i+1],to=next?.sampleOffset??(next?Math.round((next.tMs-audio.startMs)*16):Infinity);
        return from<segment.endMs*16&&to>segment.startMs*16;
      }).map(c=>({...c,targetId:visibleMark(session.scenes.find(scene=>scene.id===c.sceneId),c.targetId)?.id||null}));
      const scenes=new Map(candidates.map(c=>[c.sceneId,c])),c=scenes.size===1?[...scenes.values()][0]:null;
      const targets=new Set(candidates.map(c=>c.targetId)),timingUncertain=!!segment.timingEstimated||!!audio.metadata?.discontinuities?.length;
      const resolved=!timingUncertain&&c&&c.sceneId!=='unbound'&&session.scenes.some(scene=>scene.id===c.sceneId&&!scene.deletedAt);
      const target=resolved&&targets.size===1?[...targets][0]:null;
      session.transcript.push({id,audioId:audio.id,kind:'speech',text:segment.text,t:start/1000,startMs:start,endMs:end,timingAdjusted:!!segment.timingAdjusted,timingEstimated:!!segment.timingEstimated,providerStartMs:segment.providerStartMs,providerEndMs:segment.providerEndMs,sceneId:segment.timingEstimated?null:(resolved?c.sceneId:candidates.find(c=>c.sceneId!=='unbound')?.sceneId||null),target,unresolved:!resolved,timingUncertain,candidates:[...new Map(candidates.map(c=>[c.sceneId+'|'+c.targetId,c])).values()],updatedAt:Date.now()});
      added=true;
    });
    if(added){session.transcript.sort((a,b)=>a.t-b.t);if(!inactive)changed();}return added;
  }
  async function refreshAudio(){
    if(!revision)return;const id=session.id;
    const [snapshot,originals]=await Promise.all([API.get(id),API.originals(id)]);
    if(session.id!==id||inactive)return;
    session.recordings=snapshot.audio||[];session.originalRecordings=Array.isArray(originals)?originals:(originals.originals||[]);
    let added=false;session.recordings.forEach(audio=>{added=mergeAudio(audio)||added;});
    if(added)renderTranscript();renderRecordings();
  }
  async function retryTranscription(id){if(audioBusy)return;audioBusy=true;voiceState='transcribing';updateVoice();renderRecordings();try{const response=await API.transcribe(session.id,id);upsertAudio(response.audio);mergeAudio(response.audio);voiceState='idle';voiceError='';await persist();renderTranscript();}catch(error){voiceState='error';voiceError=transcriptionError(error);}finally{audioBusy=false;updateVoice();await refreshAudio().catch(()=>{});}}
  async function adoptCapture(capture){if(!capture?.id||!capture.source||!/^\/captures\/[a-f0-9]{32}\.png$/.test(capture.imageUrl))throw Error('invalid_capture');const existing=session.scenes.find(item=>item.captureId===capture.id);if(!existing){session.scenes.push({id:'scene-'+capture.id,captureId:capture.id,title:capture.source.kind==='program'?capture.source.name:capture.source.name,source:capture.source,app:capture.source.kind==='program'?capture.source.name:'모니터',imageUrl:capture.imageUrl,width:capture.width,height:capture.height,capturedAt:nowMs()/1000,annotations:[],note:''});}if(!noteDraft.trim()){sceneId=existing?.id||'scene-'+capture.id;targetId=null;selectedSpeech=null;}waiting=false;claimEditor();selectionBusy=false;channel?.postMessage({type:'selecting',sessionId:session.id,value:false});markContext();changed();await persist();render();}
  function deleteScene(id){
    if(noteDraft.trim())return notify('입력 중인 설명을 먼저 저장해주세요.');
    if(audioBusy||recordingStarting||originalSavePromise||recorder&&['requesting','recording','stopping'].includes(recorder.state))return notify('녹음을 종료하고 저장한 뒤 캡처를 삭제해주세요.');
    const scenes=visibleScenes(),index=scenes.findIndex(scene=>scene.id===id),selected=currentScene()?.id;if(index<0)return;
    scenes[index].deletedAt=Math.max(Date.now(),...session.scenes.map(scene=>(scene.deletedAt||0)+1));
    if(selected===id){const remaining=visibleScenes();sceneId=remaining[Math.min(index,remaining.length-1)]?.id||null;targetId=null;selectedSpeech=null;draftContext=null;}
    markContext();changed();render();notify('캡처와 연결된 설명을 제외했어요. 삭제 취소로 복원할 수 있어요.');
  }
  function restoreScene(){
    if(noteDraft.trim())return notify('입력 중인 설명을 먼저 저장해주세요.');
    const scene=session.scenes.filter(scene=>scene.deletedAt).sort((a,b)=>b.deletedAt-a.deletedAt)[0];if(!scene)return;
    delete scene.deletedAt;sceneId=scene.id;targetId=null;selectedSpeech=null;draftContext=null;
    markContext();changed();render();
  }
  async function act(action,element){if(action==='settings')return showSettings();if(action==='close')return closeDialog();if(generating||inactive)return;if(runTransition&&!['close'].includes(action))return;switch(action){case 'capture':if(runTransition)return;await activateCapture();break;case 'resume-session':if(anotherEditorActive())return notify('기존 Cuevia 창에서 기록을 이어가주세요.');waiting=false;claimEditor();render();break;case 'standby':if(noteDraft.trim())return notify('입력 중인 설명을 먼저 저장해주세요.');if(runActive())await endRun();else await persist();waiting=true;render();break;case 'tool':if(['point','arrow','rect'].includes(element.dataset.tool)){tool=element.dataset.tool;render();}break;case 'undo-mark':undoMark();break;case 'clear-marks':clearMarks();break;case 'delete-scene':deleteScene(element.dataset.id);break;case 'restore-scene':restoreScene();break;case 'scene':if(noteDraft.trim())return notify('입력 중인 설명을 먼저 저장해주세요.');sceneId=element.dataset.id;targetId=null;selectedSpeech=null;markContext();render();break;case 'speech':case 'edit':{if(noteDraft.trim())return notify('입력 중인 설명을 먼저 저장해주세요.');const item=session.transcript.find(item=>item.id===element.dataset.id);if(!item)return;sceneId=item.sceneId;targetId=visibleMark(currentScene(),item.target)?.id||null;selectedSpeech=item.id;if(action==='edit'){editingId=item.id;noteDraft=item.text;draftContext={tMs:item.t*1000,sceneId:item.sceneId,targetId:item.target||null};}markContext();render();if(action==='edit')$('#description-text')?.focus();break;}case 'cancel-edit':noteDraft='';editingId=null;draftContext=null;render();break;case 'toggle-audio':await toggleRun('audio');break;case 'toggle-text':await toggleRun('text');break;case 'history':await showHistory();break;case 'view-archive':await viewArchive(element.dataset.id);break;case 'generate':await generate();break;case 'close':closeDialog();break;case 'copy':try{if(!materials)return notify('로컬 자료가 준비된 후 복사해주세요.');if(Date.now()-Date.parse(materials.createdAt)>=retentionMs){closeDialog();await generate();return;}await navigator.clipboard.writeText(materials.prompt);element.innerHTML=icon('check')+'복사 완료';notify('파일 경로가 포함된 프롬프트를 복사했어요. 에이전트에게 붙여 넣으세요.');}catch{notify('클립보드를 사용할 수 없어요. 표시된 로컬 request.md를 직접 읽을 수 있어요.');}break;case 'retry-save':await persist();break;case 'retry-audio':await retryAudio();break;case 'retry-original':await saveOriginalAudio();break;case 'transcribe':await retryTranscription(element.dataset.id);break;}}
  document.addEventListener('click',event=>{const element=event.target.closest('[data-action]');if(element&&!element.disabled)act(element.dataset.action,element).catch(error=>notify(error.message==='revision_conflict'?'최신 기록과 충돌했어요. 저장된 내용을 다시 확인해주세요.':'작업을 마치지 못했어요. 기록은 이 창에 유지돼요.'));if(event.target.matches('.backdrop'))closeDialog();});
  document.addEventListener('submit',event=>{if(event.target.id==='settings-form'){event.preventDefault();saveSettings();return;}if(event.target.id==='composer'){event.preventDefault();submitDescription();}});
  document.addEventListener('input',event=>{if(inactive||runTransition||generating)return;if(event.target.id==='description-text'){noteDraft=event.target.value;if(!noteDraft.trim())draftContext=null;else if(!draftContext)draftContext=context();scheduleSave();}});
  document.addEventListener('keydown',event=>{if(event.isComposing)return;if(event.key==='Escape'&&dialog){event.preventDefault();closeDialog();}if((event.metaKey||event.ctrlKey)&&event.key==='Enter'&&!dialog&&document.activeElement?.id==='description-text'){event.preventDefault();submitDescription();}if(event.key==='Tab'&&dialog)trapDialogFocus(event);});
  window.addEventListener('beforeunload',event=>{expireCurrentSession();saveLocal();if(generating||runTransition||originalSavePromise||audioBusy||unsavedAudio||originalAudio||recordingStarting||recorder&&['recording','requesting','stopping'].includes(recorder.state)){event.preventDefault();event.returnValue='';}});
  window.addEventListener('pagehide',releaseLease);
  window.addEventListener('storage',event=>{if(event.key===ownerKey&&event.newValue!==editorId){saveLocal();inactive=true;waiting=true;stopRecording().catch(()=>{});if(runActive())session.runState='interrupted';setClock(nowMs(),false);render();}});
  channel?.addEventListener('message',event=>{
    const message=event.data;if(message.sessionId!==session.id)return;
    if(message.type==='capture-ack'&&message.to===editorId){captureAcks.get(message.captureId)?.();return;}
    if(message.type==='capture-offer'){
      if(!inactive&&(!waiting||runTransition||generating)){channel.postMessage({type:'capture-ack',sessionId:session.id,captureId:message.capture.id,to:message.from});pendingCaptures.push(message.capture);drainCaptures();}
      return;
    }
    if(message.type==='selecting'){selectionBusy=message.value;markContext();updateVoice();}
    if(message.type==='audio'&&!inactive)refreshAudio().catch(()=>{});
  });
  async function drainCaptures(){
    if(inactive||runTransition||generating||drainCaptures.busy)return;
    drainCaptures.busy=true;
    try{while(pendingCaptures.length)await adoptCapture(pendingCaptures.shift());}
    catch{notify('캡처를 저장하지 못했어요. 이 창에서 다시 저장해주세요.');}
    finally{drainCaptures.busy=false;}
  }
  async function receiveCapture(capture){
    let otherOwner=false;try{otherOwner=!!localStorage.getItem(ownerKey)&&localStorage.getItem(ownerKey)!==editorId;}catch{}
    if(channel&&otherOwner){
      const handled=await new Promise(resolve=>{
        const timer=setTimeout(()=>{captureAcks.delete(capture.id);resolve(false);},800);
        captureAcks.set(capture.id,()=>{clearTimeout(timer);captureAcks.delete(capture.id);resolve(true);});
        channel.postMessage({type:'capture-offer',sessionId:session.id,capture,from:editorId});
      });
      if(handled){inactive=true;waiting=true;render();return;}
      if(anotherEditorActive()){inactive=true;waiting=true;render();return;}
      try{const latest=await API.current();if(latest.session.scenes.some(scene=>scene.captureId===capture.id)){inactive=true;waiting=true;render();return;}applySnapshot(latest);}catch{}
    }
    await adoptCapture(capture);
  }
  setInterval(()=>{if(inactive||(waiting&&!runTransition&&!recordingStarting))return;renewLease();const el=$('#record-time');if(runActive()){if(el)el.textContent=clock(nowMs()/1000);if(recorder?.state==='recording'&&nowMs()-activeAudioStartMs>295000&&!runTransition)endRun().then(()=>notify('5분 분량의 녹음을 저장했어요. 이어서 녹음할 수 있어요.')).catch(()=>{});}saveLocal();},1000);
  setInterval(async()=>{
    if(inactive||activityPolling||booting)return;activityPolling=true;
    try{
      for(const event of await API.activity(activitySeq)){
        activitySeq=Math.max(activitySeq,event.seq);
        if(['selecting','capturing'].includes(event.state)){
          selectionBusy=true;if(runActive()&&session.runMode==='text')markContext();
          if(recorder?.state==='recording')recorder.markContext({...context(),tMs:Math.max(0,nowMs()-Math.max(0,Date.now()-event.atMs))});
          if(!waiting)persist().catch(()=>{});
        }else if(event.state==='captured'&&session.scenes.some(scene=>scene.captureId===event.captureId)){selectionBusy=false;markContext();
        }else if(['canceled','failed'].includes(event.state)){
          selectionBusy=false;markContext();
          if(event.state==='failed')notify('캡처하지 못했어요. 화면 접근 권한과 선택한 창을 확인해주세요.');
        }
      }
    }catch{}finally{activityPolling=false;}
  },500);
  setInterval(async()=>{if(waiting||inactive||runTransition||polling||audioBusy||!revision)return;polling=true;try{await refreshAudio();}catch{}finally{polling=false;}},3000);
  setInterval(()=>{if(expireCurrentSession())return;if(!waiting&&!inactive&&!runTransition&&!generating&&!dialog&&revision)persist().catch(()=>{});},60000);
  async function bootstrap(){try{const stored=await API.current();applySnapshot(stored);}catch(error){if(error.status!==404){saveState='error';notify('저장된 기록을 불러오지 못했어요.');}try{const local=JSON.parse(localStorage.getItem(key)||'null');if(local?.session&&Array.isArray(local.session.scenes)&&!revision){session=local.session;revision=0;const d=local.draft||{};sceneId=d.sceneId;noteDraft=d.noteDraft||'';draftContext=d.draftContext||null;editingId=d.editingId||null;setClock(session.elapsedMs||session.duration*1000,false);}}catch{}}
    try{const history=await API.activity(0),last=history.at(-1);activitySeq=last?.seq||0;selectionBusy=['selecting','capturing'].includes(last?.state);}catch{}
    try{const status=await API.transcriptionStatus();transcriptionReady=!!status.ready;}catch{}
    booting=false;render();if(params.has('settings'))await showSettings();if(params.has('captureError'))notify('캡처하지 못했어요. 화면 접근 권한과 선택한 프로그램이 열려 있는지 확인해주세요.');if(params.has('capture')){try{await receiveCapture(await API.capture(params.get('capture')));}catch{notify('캡처를 불러오지 못했어요. 단축키로 다시 시도해주세요.');}}else if(params.has('select'))await activateCapture();
  }
  render();bootstrap();
})();
