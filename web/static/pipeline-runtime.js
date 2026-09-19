'use strict';

const intentNames={respond:'Ответить',decide:'Модель решает',context:'Только контекст'};
const sourceKindNames={audio:'Звук',screen_timer:'Экран по событию'};
let sessionPollTimer=null,audioDeviceCache=null;

function ensurePipelineRuntime(p){
 p.commands||=[];p.sources||=[];p.contexts||=[];
 p.session_budget||={calls_per_minute:60,queue_capacity:4,cooldown_ms:1500,max_parallel_calls:Math.max(1,p.budget?.max_parallel_calls||3),end_to_end_ms:Math.max(1000,p.budget?.max_wall_time_ms||30000)};
 if(!p.commands.some(c=>c.action==='analyze'))p.commands.unshift({id:'screen-answer',name:'Снимок и ответ',action:'analyze',entry_node_id:p.entry_node_id,intent:'respond',capture_screen:true,wait_for_transcript_ms:0});
 return p;
}
function runtimeNodeOptions(p,allowOutput=false){
 const out={};for(const n of p.nodes||[]){if(!allowOutput&&n.type==='output')continue;out[n.id]=n.name||n.id;}return out;
}
function contextOptions(p,none='Без истории'){const out={'':none};for(const c of p.contexts||[])if(c.enabled)out[c.id]=c.name||c.id;return out;}
function sourceStatus(id){return (state.session?.sources||[]).find(s=>s.id===id);}
function activeRuntimeSources(p){return (p.sources||[]).filter(s=>s.enabled);}
function analyzeCommand(p){return (p.commands||[]).find(c=>c.action==='analyze');}

const behaviorCaptureNames={
 screen:'Текущий экран',
 system_audio:'Системный звук',
 microphone:'Микрофон',
 screen_periodic:'Периодический экран'
};
const behaviorContextRoles={
 read:'читает выбранный context',
 write:'обновляет локальный context',
 read_write:'обновляет и читает локальный context'
};

function behaviorInputLabel(input){
 if(!input?.known)return 'не определено';
 if(!input.reachable)return 'этот stage недостижим для trigger';
 const parts=[];
 if(input.audio_required)parts.push('AUDIO');
 if(input.image_required)parts.push('IMAGE');
 if(input.text_available)parts.push('TEXT / CONTEXT');
 return parts.length?parts.join(' + '):'без media input';
}
function behaviorRouteLabel(trigger){
 const nodes=trigger.reachable||[];
 return nodes.length?nodes.map(n=>n.name||n.id).join(' → '):'маршрут недоступен';
}
function behaviorAIStagesMarkup(trigger){
 const stages=trigger.ai_stages||[];
 if(!stages.length)return '<span class="behavior-local-only">AI-вызов этим событием не запускается</span>';
 return stages.map(stage=>{const c=stage.context_mode==='none'?'без local context':stage.context_mode==='named'?'context «'+(stage.context_name||stage.context_id)+'»':'context trigger';return `<div class="behavior-ai-stage"><b>${esc(stage.name||stage.id)}</b><span>${esc(behaviorInputLabel(stage.input))} · ${esc(c)}</span></div>`;}).join('');
}
function behaviorTriggerMarkup(trigger){
 const context=trigger.context_name||trigger.context_id;
 const contextText=context?`${behaviorContextRoles[trigger.context_role]||'использует context'} «${context}»`:'без local context';
 const capture=behaviorCaptureNames[trigger.capture]||trigger.capture||'без нового capture';
 const autostart=trigger.kind==='source'?`<li><b>AutoStart:</b> ${trigger.auto_start?'включён':'выключен'}</li>`:'';
 const screen=trigger.screen?`<li><b>Экран:</b> capture ${trigger.screen.capture_interval_ms} мс · context ${trigger.screen.context_interval_ms} мс · AI не чаще ${trigger.screen.trigger_interval_ms} мс${trigger.screen.changed_only?' · только изменения':''}</li>${trigger.screen.region?`<li><b>Область:</b> ${trigger.screen.region.x},${trigger.screen.region.y} · ${trigger.screen.region.width}×${trigger.screen.region.height}</li>`:''}`:'';
 return `<section class="behavior-card behavior-report-card"><h3>${esc(trigger.name||trigger.id)}</h3><ul><li><b>Захват:</b> ${esc(capture)}</li><li><b>Local context:</b> ${esc(contextText)}</li>${autostart}${screen}<li><b>Маршрут:</b> ${esc(behaviorRouteLabel(trigger))}</li></ul><div class="behavior-ai-stages"><strong>Что получает AI</strong>${behaviorAIStagesMarkup(trigger)}</div></section>`;
}
function behaviorContextMarkup(context){
 const stored=[];
 if(context.include_transcript)stored.push('transcript');
 if(context.keep_latest_screen)stored.push('последний экран');
 if(context.include_answers)stored.push('принятые ответы');
 if(context.include_summaries)stored.push('summary с provenance');
 return `<section class="behavior-card behavior-report-card"><h3>${esc(context.name||context.id)}</h3><p>${esc(stored.length?'RAM-only: '+stored.join(', '):'RAM-only context без включённых типов данных')}</p><small>До ${context.max_age_s||0} с · ${context.max_entries||0} записей · ${context.max_chars||0} символов</small></section>`;
}
function behaviorBudgetMarkup(budget){
 if(!budget)return '';
 return `<section class="behavior-card behavior-report-card"><h3>Общий budget</h3><p>AI calls: до ${budget.calls_per_minute}/мин · одновременно ${budget.max_parallel_calls} · очередь ${budget.queue_capacity} · turn до ${budget.end_to_end_ms} мс.</p></section>`;
}
function behaviorReportMarkup(report){
 const commands=(report.commands||[]).map(behaviorTriggerMarkup).join('');
 const sources=(report.sources||[]).map(behaviorTriggerMarkup).join('');
 const contexts=(report.contexts||[]).map(behaviorContextMarkup).join('');
 const warnings=(report.warnings||[]).length?`<div class="error-banner behavior-warning">${(report.warnings||[]).map(esc).join('<br>')}</div>`:'';
 return `<div class="behavior-report" data-behavior-report><p class="note">Capture, local context и AI dispatch здесь показаны отдельно. Это preview текущего графа; он ничего не захватывает и не отправляет провайдеру.</p><h3>Ручные команды</h3>${commands||'<div class="empty-run">Ручные команды не настроены.</div>'}<h3>Фоновые источники</h3>${sources||'<div class="empty-run">Фон выключен.</div>'}<h3>Локальные контексты</h3>${contexts||'<div class="empty-run">Локальная история не используется.</div>'}${behaviorBudgetMarkup(report.session_budget)}${warnings}</div>`;
}
async function showRuntimeBehavior(pipeline){
 const report=await api('/api/pipeline/behavior','POST',{pipeline:copy(pipeline)});
 modal('Поведение пайплайна',behaviorReportMarkup(report),'ИЗ ТЕКУЩЕГО ГРАФА · БЕЗ AI');
}
function renderRuntimeStrip(){
 const root=$('#runtimeStrip');if(!root)return;const p=ensurePipelineRuntime(pipe()),cmd=analyzeCommand(p),ctx=(p.contexts||[]).find(c=>c.id===cmd?.context_id),sources=activeRuntimeSources(p),paused=!!state.session?.paused;
 const running=(state.session?.sources||[]).filter(s=>s.running).length;
 root.innerHTML=`<div class="runtime-main"><button class="runtime-chip" id="editRuntimeManual"><span>РУЧНОЙ СНИМОК</span><b>Ответить сейчас${ctx?' · '+esc(ctx.name||ctx.id):' · без истории'}</b></button><button class="runtime-chip ${sources.length?'active':''}" id="editRuntimeBackground"><span>ФОН</span><b>${esc(sources.length?`${paused?'пауза · ':''}${running}/${sources.length} источников`:'выключен')}</b></button><button class="runtime-chip ${p.contexts?.length?'active':''}" id="editRuntimeContext"><span>КОНТЕКСТ</span><b>${esc(p.contexts?.length?p.contexts.map(c=>c.name||c.id).join(', '):'не используется')}</b></button></div><div class="runtime-actions"><button class="button small subtle" id="runtimeExplain">Что произойдёт?</button>${sources.length?`<button class="button small ${paused?'primary':'subtle'}" id="pauseBackground">${paused?'Возобновить фон':'Пауза фона'}</button>`:''}<button class="button small" id="openRuntimeEditor">Источники и контекст</button></div>`;
 for(const id of ['editRuntimeManual','editRuntimeBackground','editRuntimeContext'])$('#'+id).onclick=()=>openPipelineBehavior(id.replace(/^editRuntime/, '').toLowerCase());$('#openRuntimeEditor').onclick=()=>openPipelineBehavior('manual');
 $('#runtimeExplain').onclick=guarded(()=>showRuntimeBehavior(p));
 if($('#pauseBackground'))$('#pauseBackground').onclick=guarded(async()=>{const result=await api('/api/session/pause','POST',{paused:!paused});state.session={...(state.session||{}),paused:result.paused};await refreshSessionStatus(true);});
}
async function refreshSessionStatus(force=false){
 try{state.session=await api('/api/session/status');if($('#runtimeStrip'))renderRuntimeStrip();if($('#runtimeLiveState'))renderRuntimeLiveState();return state.session;}catch(e){if(force)throw e;return state.session;}
}
function pollSessionStatus(){clearTimeout(sessionPollTimer);refreshSessionStatus().finally(()=>{sessionPollTimer=setTimeout(pollSessionStatus,(state.session?.sources||[]).some(s=>s.running)?900:2500);});}

function defaultContextConfig(){return {id:uid('context'),name:'Разговор',enabled:true,max_age_s:120,max_entries:30,max_chars:24000,include_transcript:true,include_answers:false,include_summaries:false,keep_latest_screen:true,screen_max_age_s:30};}
function defaultAudioSource(p,contextID,entry){return {id:uid('audio'),name:'Системный звук',kind:'audio',enabled:true,auto_start:false,entry_node_id:entry||p.nodes.find(n=>n.type==='audio')?.id||p.entry_node_id,intent:'decide',context_id:contextID||'',audio:{mode:'system',device_id:'',device_role:'communications',segment_ms:12000,silence_ms:700,minimum_ms:180,threshold:.012}};}
function defaultScreenSource(p,contextID,entry){
 return {
  id:uid('screen-source'),name:'Экран в фоне',kind:'screen_timer',enabled:true,auto_start:false,
  entry_node_id:entry||p.nodes.find(n=>n.type==='decision')?.id||p.entry_node_id,
  intent:'context',context_id:contextID||'',interval_ms:3000,context_interval_ms:3000,trigger_interval_ms:10000,changed_only:true,debounce_ms:1000,max_wait_ms:10000
 };
}
function commandDefaults(action,p){const contextID=p.contexts?.[0]?.id||'';return {id:uid('command'),name:{screen_context:'Добавить снимок в контекст',screen_decide:'Посмотреть и решить'}[action]||'Снимок и ответ',action,entry_node_id:p.entry_node_id,intent:action==='screen_context'?'context':action==='screen_decide'?'decide':'respond',context_id:action==='analyze'?'':contextID,capture_screen:true,wait_for_transcript_ms:0};}

function ensureManualIntentEntry(action,p){
 if(action==='screen_context'){
  let n=p.nodes.find(n=>n.type==='context');if(!n){n={id:uid('context'),name:'Контекст снимка',type:'context',position:{x:280,y:410}};p.nodes.push(n);notify('Добавлен этап Context: команда сохранит снимок без AI-вызова.');}return n.id;
 }
 if(action==='screen_decide'){
  let n=p.nodes.find(n=>n.type==='decision');if(!n){n={id:uid('decision'),name:'Нужно отвечать?',type:'decision',position:{x:300,y:410},model_pool:defaultDecisionPool()};p.nodes.push(n);const target=p.nodes.find(x=>x.type==='model_pool'&&x.id!==n.id);if(target)p.edges.push({id:uid('edge'),from:n.id,to:target.id,on:['respond'],priority:10,delay_ms:0,max_visits:1});notify('Добавлен Decision. Выберите для него модель; обычный «Снимок и ответ» остаётся прямым маршрутом.');}return n.id;
 }
 return p.entry_node_id;
}
function ensureConversation(p){ensurePipelineRuntime(p);let c=p.contexts.find(c=>c.enabled);if(!c){c=defaultContextConfig();p.contexts.push(c);}return c;}

function openPipelineBehavior(section=''){
 const p=ensurePipelineRuntime(pipe());modal('Источники, команды и контекст','<div id="runtimeEditor"></div>','ПОВЕДЕНИЕ ЭТОГО ПАЙПЛАЙНА');
 renderBehaviorEditor(p,section);
}
function renderBehaviorEditor(p,section=''){
 const root=$('#runtimeEditor');if(!root)return;const cmd=analyzeCommand(p),nodes=runtimeNodeOptions(p),contexts=contextOptions(p);
 root.innerHTML=`<div class="behavior-nav"><button class="tab ${section==='manual'||!section?'active':''}" data-behavior-section="manual">Ручные команды</button><button class="tab ${section==='background'?'active':''}" data-behavior-section="background">Фоновые источники</button><button class="tab ${section==='context'?'active':''}" data-behavior-section="context">Контекст</button><button class="tab ${section==='limits'?'active':''}" data-behavior-section="limits">Лимиты сессии</button></div><div id="behaviorBody"></div>`;
 $$('[data-behavior-section]',root).forEach(b=>b.onclick=()=>renderBehaviorEditor(p,b.dataset.behaviorSection));
 const body=$('#behaviorBody');section=section||'manual';
 if(section==='manual')renderManualBehavior(body,p,cmd,nodes,contexts);
 if(section==='background')renderBackgroundBehavior(body,p,nodes,contexts);
 if(section==='context')renderContextBehavior(body,p);
 if(section==='limits')renderSessionLimits(body,p);
}
function renderManualBehavior(root,p,cmd,nodes,contexts){
 const optional=['screen_context','screen_decide'];
 const waitField=(id,value)=>field('Ждать начатую до хоткея речь, мс',id,value||0,'number','0 — не ждать. Ожидание ограничено и не включает новую речь после cutoff.');
 root.innerHTML=`<section class="behavior-card"><div class="behavior-card-head"><div><h3>Снимок и ответ</h3><p>Это прежний Ctrl+Shift+S: screenshot сразу идёт в выбранный маршрут ответа.</p></div>${tag('ОСНОВНОЙ')}</div>${selectField('Начать с этапа','manualEntry',nodes,cmd.entry_node_id)}${selectField('Контекст маршрута','manualContext',contexts,cmd.context_id||'')}${waitField('manualWait',cmd.wait_for_transcript_ms)}<div class="actions-row"><button class="button small" id="previewManualInput">Состав следующего запроса</button></div><div class="help">«Без истории» не добавляет route context. Каждый AI-этап может отдельно наследовать этот context, выбрать другой или явно работать без local context.</div></section><section class="behavior-card"><h3>Дополнительные команды снимка</h3><p class="note">Добавляйте только те намерения, которым хотите назначить отдельный хоткей.</p>${optional.map(action=>{const c=p.commands.find(x=>x.action===action),label=action==='screen_context'?'Добавить в контекст':'Посмотреть и решить';return `<div class="optional-command"><label class="switch"><input type="checkbox" data-command-enable="${action}" ${c?'checked':''}>${label}</label>${c?`<div class="form-row">${selectField('Маршрут','cmdEntry_'+action,nodes,c.entry_node_id)}${selectField('Контекст','cmdContext_'+action,contexts,c.context_id||'')}</div>${waitField('cmdWait_'+action,c.wait_for_transcript_ms)}<button class="button small subtle" data-command-preview="${action}">Состав запроса</button>`:''}</div>`}).join('')}</section>`;
 $('#manualEntry').onchange=e=>{cmd.entry_node_id=e.target.value;dirty();};$('#manualContext').onchange=e=>{cmd.context_id=e.target.value;dirty();};
 $('#manualWait').onchange=e=>{cmd.wait_for_transcript_ms=Math.max(0,Math.round(finite(e.target.value)));dirty();};
 $('#previewManualInput').onclick=guarded(()=>showCommandPreview('analyze'));
 $$('[data-command-enable]',root).forEach(el=>el.onchange=()=>{const action=el.dataset.commandEnable,i=p.commands.findIndex(c=>c.action===action);if(el.checked&&i<0){const c=ensureConversation(p),add=commandDefaults(action,p);add.context_id=c.id;add.entry_node_id=ensureManualIntentEntry(action,p);p.commands.push(add);}else if(!el.checked&&i>=0)p.commands.splice(i,1);dirty();renderBehaviorEditor(p,'manual');});
 for(const action of optional){const c=p.commands.find(c=>c.action===action);if(!c)continue;$('#cmdEntry_'+action).onchange=e=>{c.entry_node_id=e.target.value;dirty();};$('#cmdContext_'+action).onchange=e=>{c.context_id=e.target.value;dirty();};$('#cmdWait_'+action).onchange=e=>{c.wait_for_transcript_ms=Math.max(0,Math.round(finite(e.target.value)));dirty();};}
 $$('[data-command-preview]',root).forEach(button=>button.onclick=guarded(()=>showCommandPreview(button.dataset.commandPreview)));
}
async function showCommandPreview(action){
 if(state.dirty)throw new Error('Сначала сохраните настройки: preview показывает применённую ревизию.');
 const preview=await api('/api/session/command/preview?action='+encodeURIComponent(action));
 const rows=(preview.contexts||[]).map(c=>`<tr><td>${esc(c.name||c.id)}</td><td>${c.entries}</td><td>${c.gaps}</td><td>${c.has_screen?'да · '+(c.screen_age_ms/1000).toFixed(1)+' с':'нет'}</td><td>${c.generation}</td></tr>`).join('');
 const stages=Object.entries(preview.stage_contexts||{}).map(([node,value])=>{const label=value.mode==='none'?'без local context':value.mode==='named'?((pipe().contexts||[]).find(c=>c.id===value.context_id)?.name||value.context_id):'контекст триггера';return `<li><span class="mono">${esc(node)}</span> → ${esc(label)}</li>`;}).join('')||'<li>AI-этапы не используют отдельную context policy.</li>';
 modal('Состав следующего запроса',`<p class="note">Метаданные применённой ревизии. Capture и AI сейчас не запускаются.</p><div class="preview-summary"><span>${preview.capture_screen?'Текущий экран будет закреплён по команде':'Без нового screen capture'}</span><span>Ожидание речи: ${preview.wait_for_transcript_ms||0} мс</span></div><div class="table-wrap"><table class="queue-table"><thead><tr><th>Context</th><th>Записей</th><th>Gap</th><th>Экран</th><th>Generation</th></tr></thead><tbody>${rows||'<tr><td colspan="5">Local context не выбран</td></tr>'}</tbody></table></div><h4>Context AI-этапов</h4><ul>${stages}</ul>`,'БЕЗ CAPTURE И AI');
}
function renderBackgroundBehavior(root,p,nodes,contexts){
 const sources=p.sources||[];
 root.innerHTML=`<div class="behavior-intro"><div><h3>Фоновые источники</h3><p>Источник может только обновлять контекст, просить модель решить насчёт ответа или запускать ответ. Получение аудиоблоков само по себе не является AI-вызовом.</p></div><div class="tool-group"><button class="button small primary" id="addAudioAssistant">+ STT → решение</button><button class="button small" id="addOmniAssistant">+ Одна omni-модель</button><button class="button small" id="addAudioSource">+ Звук</button><button class="button small" id="addScreenSource">+ Экран</button></div></div><div id="sourceCards">${sources.map((s,i)=>sourceEditorCard(s,i,p,nodes,contexts)).join('')||'<div class="empty-run">Фон выключен. Обычный screenshot продолжает работать без этих источников.</div>'}</div><div class="session-control"><span id="runtimeLiveState"></span>${sources.length?'<button class="button small" id="runtimePauseInDialog">Пауза / возобновить фон</button>':''}</div>`;
 bindSourceCards(root,p);$('#addAudioAssistant').onclick=()=>{addAudioAssistantTemplate(p);dirty();renderGraph();renderBehaviorEditor(p,'background');};$('#addOmniAssistant').onclick=()=>{addOmniAssistantTemplate(p);dirty();renderGraph();renderBehaviorEditor(p,'background');};$('#addAudioSource').onclick=()=>{const c=ensureConversation(p);p.sources.push(defaultAudioSource(p,c.id));dirty();renderBehaviorEditor(p,'background');};$('#addScreenSource').onclick=()=>{const c=ensureConversation(p);p.sources.push(defaultScreenSource(p,c.id));dirty();renderBehaviorEditor(p,'background');};
 if($('#runtimePauseInDialog'))$('#runtimePauseInDialog').onclick=guarded(async()=>{await api('/api/session/pause','POST',{paused:!state.session?.paused});await refreshSessionStatus(true);});renderRuntimeLiveState();
}
function sourceEditorCard(s,i,p,nodes,contexts){
 const st=sourceStatus(s.id),running=!!st?.running;let fields=`<div class="form-row">${field('Название','srcName_'+i,s.name)}${selectField('Когда новые данные готовы','srcIntent_'+i,{context:'Только обновить контекст',decide:'Пусть модель решит, отвечать ли',respond:'Сразу готовить ответ'},s.intent)}</div><div class="form-row">${selectField('Начать с этапа','srcEntry_'+i,nodes,s.entry_node_id)}${selectField('Контекст','srcContext_'+i,contexts,s.context_id||'')}</div>`;
 if(s.kind==='audio'){const a=s.audio||(s.audio={mode:'system',device_id:'',device_role:'communications',segment_ms:12000,silence_ms:700,minimum_ms:180,threshold:.012});fields+=`<div class="form-row">${selectField('Что слушать','srcMode_'+i,{system:'Системный звук — то, что слышу',microphone:'Микрофон'},a.mode)}${selectField('Устройство по умолчанию','srcRole_'+i,{multimedia:'Основное устройство',communications:'Устройство связи'},a.device_role||'multimedia')}</div><div class="field"><label>Конкретное устройство (необязательно)</label><select id="srcDevice_${i}"><option value="">Следовать выбранному устройству по умолчанию</option></select><small>Фиксированное устройство не заменяется другим молча, если оно исчезнет.</small></div><details><summary>Границы фрагмента и фильтр сигнала</summary><div class="form-row">${field('Макс. фрагмент, мс','srcSegment_'+i,a.segment_ms,'number')}${field('Пауза завершения, мс','srcSilence_'+i,a.silence_ms,'number')}</div><div class="form-row">${field('Минимум сигнала, мс','srcMinimum_'+i,a.minimum_ms,'number')}${field('Порог RMS','srcThreshold_'+i,a.threshold,'number')}</div><p class="note">RMS — только технический фильтр тишины, не распознавание речи. Музыка и уведомления тоже могут пройти этот порог.</p></details><div class="actions-row"><button class="button small" data-audio-preview="${i}">Проверить локальный захват</button><span id="audioPreviewState_${i}" class="dim">Без AI-запросов</span></div><div id="audioPreview_${i}"></div>`;}
 if(s.kind==='screen_timer')fields+=screenSourceFields(s,i);
 const liveState=st?runtimeAudioStateParts(st).join(' · '):'';return `<section class="behavior-card source-card" data-source-index="${i}"><div class="behavior-card-head"><div><h3>${esc(s.name||sourceKindNames[s.kind])}</h3><p>${esc(sourceKindNames[s.kind]||s.kind)} · ${running?'работает':'остановлен'}${liveState?' · '+esc(liveState):''}${st?.last_diagnostic?' · диагностика: '+esc(st.last_diagnostic):''}${st?.last_error?' · ошибка: '+esc(st.last_error):''}</p></div><div class="tool-group"><label class="switch"><input type="checkbox" id="srcEnabled_${i}" ${s.enabled?'checked':''}>Настроен</label><button class="button small" data-source-run="${i}" ${!s.enabled?'disabled':''}>${running?'Остановить сейчас':'Запустить сейчас'}</button><button class="icon-button" data-source-remove="${i}" aria-label="Удалить источник">×</button></div></div><label class="switch"><input type="checkbox" id="srcAuto_${i}" ${s.auto_start?'checked':''}>Запускать автоматически вместе с AIOverlay</label>${fields}</section>`;
}
function bindSourceCards(root,p){
 (p.sources||[]).forEach((s,i)=>{
  const set=(id,key,numeric=false,obj=s)=>{const el=$('#'+id);if(el)el.onchange=()=>{obj[key]=numeric?finite(el.value):el.value;dirty();renderRuntimeStrip();};};set('srcName_'+i,'name');set('srcIntent_'+i,'intent');set('srcEntry_'+i,'entry_node_id');set('srcContext_'+i,'context_id');$('#srcEnabled_'+i).onchange=e=>{s.enabled=e.target.checked;dirty();renderBehaviorEditor(p,'background');};$('#srcAuto_'+i).onchange=e=>{s.auto_start=e.target.checked;dirty();};
  if(s.kind==='audio'){const a=s.audio;set('srcMode_'+i,'mode',false,a);set('srcRole_'+i,'device_role',false,a);set('srcSegment_'+i,'segment_ms',true,a);set('srcSilence_'+i,'silence_ms',true,a);set('srcMinimum_'+i,'minimum_ms',true,a);set('srcThreshold_'+i,'threshold',true,a);loadAudioDevices($('#srcDevice_'+i),a);$('#srcDevice_'+i).onchange=e=>{a.device_id=e.target.value;dirty();};}
  if(s.kind==='screen_timer')bindScreenSource(s,i,set);
 });
 $$('[data-source-remove]',root).forEach(b=>b.onclick=()=>{const i=Number(b.dataset.sourceRemove),s=p.sources[i];if(confirm(`Удалить источник «${s.name}»?`)){p.sources.splice(i,1);dirty();renderBehaviorEditor(p,'background');renderRuntimeStrip();}});
 $$('[data-source-run]',root).forEach(b=>b.onclick=guarded(async()=>{const i=Number(b.dataset.sourceRun),s=p.sources[i],running=!!sourceStatus(s.id)?.running;if(state.dirty)throw new Error('Сначала сохраните и примените конфигурацию источника.');await api('/api/session/source','POST',{id:s.id,running:!running});await refreshSessionStatus(true);renderBehaviorEditor(p,'background');}));
 $$('[data-audio-preview]',root).forEach(b=>b.onclick=guarded(()=>previewAudioSource(p,Number(b.dataset.audioPreview))));
}
function screenSourceFields(s,i){
 const region=s.screen_region;
 return `<div class="section-line"><h4>ЧАСТОТЫ ЭКРАНА</h4></div><div class="form-row">${field('Local capture, мс','srcInterval_'+i,s.interval_ms||3000,'number')}${field('Обновлять context, мс','srcContextInterval_'+i,s.context_interval_ms||s.interval_ms||3000,'number')}${field('Не чаще AI trigger, мс','srcTriggerInterval_'+i,s.trigger_interval_ms||s.interval_ms||3000,'number')}</div><p class="note">Capture сам по себе не является AI-вызовом. Context может обновляться чаще, чем запускается Decision/Answer.</p><label class="switch"><input type="checkbox" id="srcChanged_${i}" ${s.changed_only?'checked':''}>Только при заметном изменении</label><details><summary>Стабилизация изменений</summary><div class="form-row">${field('Debounce, мс','srcDebounce_'+i,s.debounce_ms||0,'number')}${field('Макс. ожидание, мс','srcMaxWait_'+i,s.max_wait_ms||0,'number')}</div><p class="note">Debounce ждёт устойчивый кадр. Максимальное ожидание не даёт видео или анимации остановить обновления навсегда.</p></details><details ${region?'open':''}><summary>Область capture</summary><label class="switch"><input type="checkbox" id="srcRegionEnabled_${i}" ${region?'checked':''}>Ограничить прямоугольной областью</label>${region?`<div class="form-row">${field('X','srcRegionX_'+i,region.x,'number')}${field('Y','srcRegionY_'+i,region.y,'number')}</div><div class="form-row">${field('Ширина','srcRegionW_'+i,region.width,'number')}${field('Высота','srcRegionH_'+i,region.height,'number')}</div>`:''}<p class="note">Crop выполняется только в RAM после разрешённого capture. Координаты относятся к полученному кадру; выход за его границы останавливает этот frame с понятной ошибкой.</p></details>`;
}
function bindScreenSource(source,index,set){
 set('srcInterval_'+index,'interval_ms',true);
 set('srcContextInterval_'+index,'context_interval_ms',true);
 set('srcTriggerInterval_'+index,'trigger_interval_ms',true);
 set('srcDebounce_'+index,'debounce_ms',true);
 set('srcMaxWait_'+index,'max_wait_ms',true);
 $('#srcChanged_'+index).onchange=event=>{source.changed_only=event.target.checked;dirty();};
 $('#srcRegionEnabled_'+index).onchange=event=>{if(event.target.checked)source.screen_region={x:0,y:0,width:1280,height:720};else delete source.screen_region;dirty();renderBehaviorEditor(pipe(),'background');};
 if(source.screen_region){for(const [id,key]of [['srcRegionX_','x'],['srcRegionY_','y'],['srcRegionW_','width'],['srcRegionH_','height']]){const el=$('#'+id+index);if(el)el.onchange=e=>{source.screen_region[key]=Math.round(finite(e.target.value));dirty();};}}
}
async function loadAudioDevices(select,a){if(!select)return;try{audioDeviceCache ||= await api('/api/audio/devices');const items=(audioDeviceCache||[]).filter(d=>d.mode===a.mode);select.innerHTML='<option value="">Следовать выбранному устройству по умолчанию</option>'+items.map(d=>`<option value="${esc(d.id)}" ${d.id===a.device_id?'selected':''}>${esc(d.name)}</option>`).join('');if(a.device_id&&!items.some(d=>d.id===a.device_id)){const op=document.createElement('option');op.value=a.device_id;op.selected=true;op.textContent='Сохранённое устройство сейчас недоступно';select.append(op);}}catch{select.innerHTML='<option value="">Список устройств доступен в Windows-приложении</option>';}}
async function previewAudioSource(p,i){const s=p.sources[i];if(!s?.audio)return;const status=$('#audioPreviewState_'+i);if((state.session?.sources||[]).some(s=>s.running)&&!state.session?.paused){if(!confirm('На время локальной проверки приостановить фон? Он останется на паузе, пока вы явно не возобновите его.'))return;await api('/api/session/pause','POST',{paused:true});await refreshSessionStatus(true);}status.textContent='Запись 2 секунды…';const result=await api('/api/audio/preview','POST',{config:s.audio,duration_ms:2000});status.textContent=`Получено ${(result.duration_ms/1000).toFixed(1)} с · RMS ${Number(result.rms).toFixed(3)} · peak ${Number(result.peak).toFixed(3)}`;const root=$('#audioPreview_'+i);root.innerHTML=`<audio controls preload="metadata" src="data:audio/wav;base64,${result.wav_base64}"></audio>${state.session?.paused?'<p class="note">Фон остаётся на паузе, чтобы воспроизведение теста не попало обратно в system loopback.</p>':''}`;}
function runtimeOutcomeLabel(outcome){return {no_action:'без ответа по решению модели',respond:'ответ опубликован',context_only:'обновлён только контекст',rate_limited:'лимит провайдера',timeout:'таймаут',any_error:'ошибка обработки'}[outcome]||outcome||'';}
function runtimeSuppressionLabel(reason){return {cooldown_after_auto_answer:'AI на паузе после автоответа',studio_visible_without_capture_exclusion:'кадр пропущен: Studio видима без подтверждённого capture exclusion',studio_capture_safety_timeout:'кадр пропущен: Studio не подтвердила безопасность capture'}[reason]||reason||'';}
const audioTransportLabels={starting:'transport запускается',alive:'transport жив',recovering:'transport переподключается',stopped:'transport остановлен',unavailable:'transport недоступен'};
const audioSignalLabels={unknown:'сигнал ещё не определён',no_packets:'пакетов сейчас нет',silent:'WASAPI: тишина',data:'аудиоданные поступают'};
function runtimeAudioStateParts(source){
 const parts=[];
 if(source.transport_state)parts.push(audioTransportLabels[source.transport_state]||source.transport_state);
 if(source.signal_state)parts.push(audioSignalLabels[source.signal_state]||source.signal_state);
 return parts;
}
function runtimeSourceSummary(source){
 const parts=[`${source.name}: ${source.running?'слушает/наблюдает':'остановлен'}`,...runtimeAudioStateParts(source)];
 if(source.last_outcome)parts.push(runtimeOutcomeLabel(source.last_outcome));
 if(source.last_suppression)parts.push(runtimeSuppressionLabel(source.last_suppression));
 if(source.transcript_lag_ms)parts.push(`lag ${(source.transcript_lag_ms/1000).toFixed(1)}с`);
 if(source.dropped)parts.push(`пропусков ${source.dropped}`);
 if(source.last_diagnostic)parts.push(`диагностика: ${source.last_diagnostic}`);
 if(source.last_error)parts.push(`ошибка: ${source.last_error}`);
 return parts.join(' · ');
}
function renderRuntimeLiveState(){
 const root=$('#runtimeLiveState');if(!root)return;
 const session=state.session;if(!session){root.textContent='Состояние runtime загружается…';return;}
 const sources=session.sources||[];
 root.textContent=session.paused?'Фон на паузе':sources.length?sources.map(runtimeSourceSummary).join(' · '):'Фоновые источники не запущены';
}

function renderContextBehavior(root,p){
 const cards=(p.contexts||[]).map((c,i)=>{
  const screenAge=c.keep_latest_screen?field('Экран актуален, секунд','ctxScreenAge_'+i,c.screen_max_age_s,'number'):'';
  return `<section class="behavior-card"><div class="behavior-card-head"><h3>${esc(c.name||c.id)}</h3><button class="icon-button" data-context-remove="${i}" aria-label="Удалить контекст">×</button></div><div class="form-row">${field('Название','ctxName_'+i,c.name)}<label class="switch"><input type="checkbox" id="ctxEnabled_${i}" ${c.enabled?'checked':''}>Использовать</label></div><div class="form-row">${field('Хранить, секунд','ctxAge_'+i,c.max_age_s,'number')}${field('Макс. записей','ctxEntries_'+i,c.max_entries,'number')}${field('Макс. символов','ctxChars_'+i,c.max_chars,'number')}</div><label class="switch"><input type="checkbox" id="ctxTranscript_${i}" ${c.include_transcript?'checked':''}>Разрешать transcript в этом контексте</label><label class="switch"><input type="checkbox" id="ctxAnswers_${i}" ${c.include_answers?'checked':''}>Хранить принятые ответы ассистента</label><label class="switch"><input type="checkbox" id="ctxSummaries_${i}" ${c.include_summaries?'checked':''}>Разрешать summary с provenance в следующих запросах</label><small>Summary хранится как отдельная derived-запись с derived_from; чтение выключено по умолчанию.</small><label class="switch"><input type="checkbox" id="ctxScreen_${i}" ${c.keep_latest_screen?'checked':''}>Хранить последний экран в RAM</label>${screenAge}<div class="actions-row"><button class="button small danger subtle" data-context-clear="${esc(c.id)}">Очистить разговор сейчас</button><span class="dim">Очистка не может отозвать уже отправленные cloud-данные.</span></div></section>`;
 }).join('');
 root.innerHTML=`<div class="behavior-intro"><div><h3>Контексты</h3><p>Хранение данных и передача конкретной модели — разные вещи. Контекст здесь RAM-only; внешняя realtime-сессия может иметь собственную remote memory.</p></div><button class="button small" id="addContext">+ Контекст</button></div>${cards||'<div class="empty-run">История выключена. Обычный screenshot отправляет только текущий снимок.</div>'}`;
 $('#addContext').onclick=()=>{p.contexts.push(defaultContextConfig());dirty();renderBehaviorEditor(p,'context');};
 (p.contexts||[]).forEach((c,i)=>{
  const set=(id,key,numeric=false)=>{const e=$('#'+id);if(e)e.onchange=()=>{c[key]=numeric?finite(e.value):e.value;dirty();};};
  set('ctxName_'+i,'name');set('ctxAge_'+i,'max_age_s',true);set('ctxEntries_'+i,'max_entries',true);set('ctxChars_'+i,'max_chars',true);set('ctxScreenAge_'+i,'screen_max_age_s',true);
  for(const [id,key]of [['ctxEnabled_','enabled'],['ctxTranscript_','include_transcript'],['ctxAnswers_','include_answers'],['ctxSummaries_','include_summaries'],['ctxScreen_','keep_latest_screen']]){const e=$('#'+id+i);if(e)e.onchange=()=>{c[key]=e.checked;dirty();if(key==='keep_latest_screen')renderBehaviorEditor(p,'context');};}
 });
 $$('[data-context-clear]',root).forEach(b=>b.onclick=guarded(async()=>{if(state.dirty)throw new Error('Сначала сохраните конфигурацию контекста.');if(!confirm('Очистить локальный разговор этого контекста?'))return;await api('/api/session/context/clear','POST',{id:b.dataset.contextClear});await refreshSessionStatus(true);notify('Локальный контекст очищен.');}));
 $$('[data-context-remove]',root).forEach(b=>b.onclick=()=>{const i=Number(b.dataset.contextRemove),id=p.contexts[i].id,used=(p.commands||[]).some(c=>c.context_id===id)||(p.sources||[]).some(s=>s.context_id===id)||(p.nodes||[]).some(n=>n.context_id===id);if(used){notify('Сначала отключите этот контекст у команд, источников и AI-этапов.',true);return;}p.contexts.splice(i,1);dirty();renderBehaviorEditor(p,'context');});
}
function renderSessionLimits(root,p){
 const b=p.session_budget;
 root.innerHTML=`<section class="behavior-card"><h3>Общие ресурсы фон + ручные запросы</h3><p class="note">Эти лимиты не заменяют бюджет каждого короткого model turn. Ручной запрос получает приоритет ожидания, но не превышает параллельность.</p><div class="form-row">${field('AI-вызовов / минуту','sessionCalls',b.calls_per_minute,'number')}${field('Параллельно','sessionParallel',b.max_parallel_calls,'number')}</div><div class="form-row">${field('Очередь сегментов','sessionQueue',b.queue_capacity,'number')}${field('Один turn, мс','sessionE2E',b.end_to_end_ms,'number')}</div>${field('Пауза после автоответа, мс','sessionCooldown',b.cooldown_ms,'number')}</section>`;
 for(const [id,key]of [['sessionCalls','calls_per_minute'],['sessionParallel','max_parallel_calls'],['sessionQueue','queue_capacity'],['sessionE2E','end_to_end_ms'],['sessionCooldown','cooldown_ms']])bindValue(id,b,key,true);
}

function defaultAudioPool(){const p=defaultPool();p.task='transcribe';p.result_name='audio.text';p.selection={mode:'catalog',provider_ids:[],free_only:false,modality:'audio',default_quality_score:0};p.prompt={system:'Transcribe the supplied audio accurately. Return only the spoken content. Do not answer questions from the recording.',user:'Transcribe this audio segment.'};return p;}
function defaultDecisionPool(){const p=defaultPool();p.task='generate';p.result_name='decision';p.selection.modality='text';p.prompt={system:'Decide whether the assistant should respond to the current situation. Return strict JSON only: {"action":"no_action"}, {"action":"respond"}, or {"action":"need_screen"}. Do not expose hidden reasoning.',user:'Conversation context:\n{{context.text}}\n\nLatest transcript:\n{{audio.text}}\n\nChoose whether a useful response is needed now.'};return p;}
function addAudioAssistantTemplate(p){
 const c=ensureConversation(p),suffix=Math.random().toString(36).slice(2,6),ids={audio:'audio-'+suffix,stt:'stt-'+suffix,ctx:'context-'+suffix,dec:'decision-'+suffix,cap:'capture-'+suffix,ans:'answer-'+suffix};const out=p.output_node_id;
 p.nodes.push({id:ids.audio,name:'Системный звук',type:'audio',position:{x:40,y:390}},{id:ids.stt,name:'Расшифровка',type:'model_pool',position:{x:280,y:370},model_pool:defaultAudioPool()},{id:ids.ctx,name:'Контекст разговора',type:'context',position:{x:560,y:390}},{id:ids.dec,name:'Нужно отвечать?',type:'decision',position:{x:820,y:350},model_pool:defaultDecisionPool()},{id:ids.cap,name:'Свежий экран',type:'capture_screen',position:{x:1080,y:500}},{id:ids.ans,name:'Ответ по ситуации',type:'model_pool',position:{x:1080,y:300},model_pool:defaultPool()});
 p.nodes.find(n=>n.id===ids.ans).model_pool.prompt={system:'Answer the user helpfully using the supplied conversation and visual context. Do not invent details.',user:'Conversation:\n{{context.text}}\nLatest speech:\n{{audio.text}}\nVisual context:\n{{vision.text}}\n\nProvide the useful answer now.'};
 const edge=(from,to,on)=>({id:uid('edge'),from,to,on,priority:10,delay_ms:0,max_visits:1});p.edges.push(edge(ids.audio,ids.stt,['success']),edge(ids.stt,ids.ctx,['success']),edge(ids.ctx,ids.dec,['success']),edge(ids.dec,ids.ans,['respond']),edge(ids.dec,ids.cap,['need_screen']),edge(ids.cap,ids.ans,['success']),edge(ids.ans,out,['success']));
 p.sources.push(defaultAudioSource(p,c.id,ids.audio));p.sources.at(-1).name='Разговор';p.sources.at(-1).intent='decide';notify('Добавлен редактируемый сценарий: звук → расшифровка → контекст → решение → ответ. Выберите модели и явно запустите источник.');
}

function defaultOmniDecisionPool(){const p=defaultDecisionPool();p.selection.modality='auto';p.prompt={system:'You are a quiet multimodal assistant. Listen to the supplied audio and use visual/context data when present. Return strict JSON only. If no useful reply is needed: {"action":"no_action","transcript":"..."}. If a reply is useful and current visual data is sufficient: {"action":"respond","text":"...","transcript":"..."}. If a useful reply requires a fresh screen: {"action":"need_screen","transcript":"..."}. Never invent unreadable visual details.',user:'Context:\n{{context.text}}\n\nDecide whether to respond now. The current audio is attached separately; a screenshot may also be attached.'};return p;}
function addOmniAssistantTemplate(p){
 const c=ensureConversation(p),suffix=Math.random().toString(36).slice(2,6),ids={audio:'omni-audio-'+suffix,ctx:'omni-context-'+suffix,dec:'omni-decision-'+suffix,cap:'omni-capture-'+suffix},out=p.output_node_id;
 p.nodes.push({id:ids.audio,name:'Live audio segment',type:'audio',position:{x:50,y:520}},{id:ids.ctx,name:'Экранный контекст',type:'context',position:{x:300,y:590}},{id:ids.dec,name:'Omni: решить / ответить',type:'decision',position:{x:330,y:470},model_pool:defaultOmniDecisionPool()},{id:ids.cap,name:'Обновить экран',type:'capture_screen',position:{x:650,y:590}});
 const edge=(from,to,on)=>({id:uid('edge'),from,to,on,priority:10,delay_ms:0,max_visits:1});p.edges.push(edge(ids.audio,ids.dec,['success']),edge(ids.dec,out,['respond']),edge(ids.dec,ids.cap,['need_screen']),edge(ids.cap,ids.dec,['success']));
 const audio=defaultAudioSource(p,c.id,ids.audio);audio.name='Omni audio';audio.intent='decide';p.sources.push(audio);const screen=defaultScreenSource(p,c.id,ids.ctx);screen.name='Экранный контекст для omni';screen.intent='context';p.sources.push(screen);notify('Добавлен редактируемый omni-сценарий. Raw audio и последний разрешённый экран поступают одной совместимой модели; AutoStart выключен до явного запуска.');
}
