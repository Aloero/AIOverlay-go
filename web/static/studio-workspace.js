'use strict';

// Draft history lives only in this WebView session. It neither replays requests
// nor rolls back independent provider saves / the running application's state.
class DraftHistory {
 constructor(){this.reset(null);}
 reset(config){this.undoStack=[];this.redoStack=[];this.current=config?this.encode(config):null;this.lastKey='';this.lastAt=0;}
 encode(config){return JSON.stringify({pipelines:config.pipelines,overlay:config.overlay,app:config.app,control_center:config.control_center,vpn:config.vpn});}
 record(config,key=''){
  const next=this.encode(config);if(next===this.current)return;
  if(this.current!==null){const now=Date.now(),merge=key&&key===this.lastKey&&now-this.lastAt<650;if(!merge)this.undoStack.push(this.current);this.lastKey=key;this.lastAt=now;}
  this.current=next;this.redoStack=[];
  // Bound both count and approximate UTF-16 memory, not only action count.
  let bytes=this.current.length*2+this.undoStack.reduce((n,s)=>n+s.length*2,0);
  while(this.undoStack.length>40||bytes>2*1024*1024&&this.undoStack.length){bytes-=this.undoStack.shift().length*2;}
 }
 move(config,direction){const from=direction==='undo'?this.undoStack:this.redoStack,to=direction==='undo'?this.redoStack:this.undoStack;if(!from.length)return false;to.push(this.current);this.current=from.pop();Object.assign(config,JSON.parse(this.current));this.lastKey='';return true;}
}
function flushFocusedEditor(){
 const el=document.activeElement;
 if(el?.isConnected&&el.matches('input,textarea,select'))el.dispatchEvent(new Event('change',{bubbles:true}));
}
const draftHistory=new DraftHistory();
let appliedConfig=null,appliedJSON='',workspaceInfo=null,workspaceInfoAt=0,workspaceInfoPromise=null;
function configDigest(c){return JSON.stringify(c);}
function markApplied(snapshot,resetHistory=true){
 appliedConfig=copy(snapshot);appliedJSON=configDigest(appliedConfig);
 if(resetHistory){draftHistory.reset(snapshot);if(configDigest(state.config)!==appliedJSON)draftHistory.record(state.config);}
 updateApplyState();
}
function recordDraft(){
 if(!state.config)return;
 const el=document.activeElement,key=el?.matches('input,textarea')?(el.id||el.dataset.mfield||''):'';
 draftHistory.record(state.config,key);
 updateApplyState();
}
function updateApplyState(){
 if(!state.config)return;
 const dirtyNow=appliedConfig?configDigest(state.config)!==appliedJSON:state.dirty;
 state.dirty=dirtyNow;
 // Индикатор и кнопка живут только вместе с черновиком: «Применено» — не то
 // состояние, которое стоит показывать, а кнопка без сохранения бессмысленна.
 const indicator=$('#saveState');if(indicator){indicator.className='save-state'+(dirtyNow?' dirty':' hidden');indicator.innerHTML='<i></i>Черновик';}
 const save=$('#saveConfig');if(save){save.classList.toggle('hidden',!dirtyNow);save.disabled=!!savePromise||!!importPromise||!!state.job?.running;save.title=state.job?.running?'Сначала остановите текущий запрос. Черновик можно редактировать.':'Сохранить config.json и применить к следующему снимку (Ctrl+S)';}
 const applied=appliedConfig?.pipelines.find(p=>p.id===appliedConfig.active_pipeline_id),summary=$('#appliedSummary');
 if(summary){summary.textContent='Следующий снимок: '+(applied?.name||'—');summary.title='Оверлей использует сохранённую конфигурацию, а не открытый черновик.';}
 const hint=$('#applyHint');if(hint){hint.textContent=state.job?.running?'Запрос выполняется. Применение доступно после завершения.':dirtyNow?'Изменения ещё не применены.':'Сохранённая конфигурация';hint.classList.toggle('warning-text',dirtyNow);}
 const current=$('#currentExecution');if(current){const tr=state.job?.trace;current.textContent=state.job?.running?'Сейчас: '+(tr?.pipeline_name||'подготовка')+(tr?.config_revision?' · r'+tr.config_revision:''):'';current.hidden=!state.job?.running;}
 if($('#importConfig'))$('#importConfig').disabled=!!importPromise;
 if($('#undoDraft'))$('#undoDraft').disabled=!draftHistory.undoStack.length||!!savePromise||!!importPromise||!!dialogDraft;
 if($('#redoDraft'))$('#redoDraft').disabled=!draftHistory.redoStack.length||!!savePromise||!!importPromise||!!dialogDraft;
 if($('#restoreDraft'))$('#restoreDraft').disabled=!dirtyNow||!!savePromise||!!importPromise;
}
async function refreshWorkspaceState(force=false){
 if(workspaceInfoPromise)return workspaceInfoPromise;
 if(!force&&Date.now()-workspaceInfoAt<1800)return workspaceInfo;
 workspaceInfoPromise=(async()=>{workspaceInfo=await api('/api/studio/state');workspaceInfoAt=Date.now();updateCredentialBadges();updateApplyState();return workspaceInfo;})();
 try{return await workspaceInfoPromise;}finally{workspaceInfoPromise=null;}
}
function initWorkspace(){
 markApplied(state.config);applyWorkspaceLayout();
 for(const [id,text]of Object.entries({undoDraft:'Отменяет правку черновика. Не отменяет выполненный AI-запрос и отдельно сохранённые подключения.',redoDraft:'Повторяет отменённую правку черновика.',restoreDraft:'Загружает сохранённый config.json. Текущий запрос и история не отменяются.',findSetting:'Поиск открывает нужное поле без переключения активного пайплайна. Ctrl+K.',saveConfig:'Записывает и применяет настройки к следующему снимку. Выполняющийся запрос использует прежнюю конфигурацию.'}))if($('#'+id))$('#'+id).dataset.help=text;
 $('#undoDraft').onclick=()=>moveDraftHistory('undo');$('#redoDraft').onclick=()=>moveDraftHistory('redo');
 $('#restoreDraft').onclick=guarded(restoreSavedDraft);
 $('#toggleSidebar').onclick=()=>{const w=workspaceSettings();w.sidebar_collapsed=!w.sidebar_collapsed;dirty();applyWorkspaceLayout();};
 $('#findSetting').onclick=guarded(openSettingSearch);$('#aboutStudio').onclick=guarded(openAboutStudio);
 $$('[data-page]').forEach(el=>{el.title=el.textContent.trim();});
 document.addEventListener('keydown',e=>{
  if(!(e.ctrlKey||e.metaKey)||e.altKey)return;
  const key=e.key.toLowerCase();
  if(key==='k'){e.preventDefault();guarded(openSettingSearch)();return;}
  if((key==='z'||key==='y')&&!e.target.closest('input,textarea,[contenteditable=true]')&&!$('#dialog').open){e.preventDefault();moveDraftHistory(key==='y'||e.shiftKey?'redo':'undo');}
 });
 window.addEventListener('resize',()=>{applyWorkspaceLayout();if($('#graphLinks'))drawLinks();});
 refreshWorkspaceState(true).catch(failure);
}
function moveDraftHistory(direction){
 if(savePromise||importPromise||dialogDraft){notify('Сначала завершите сохранение или закройте диалог.');return;}
 if(!draftHistory.move(state.config,direction))return;
 normalizeConfig(state.config);configRevision++;state.dirty=configDigest(state.config)!==appliedJSON;
 if(!state.config.pipelines.some(p=>p.id===state.pipeline))state.pipeline=state.config.active_pipeline_id;
 state.edge=null;state.node=null;state.meta.clear();renderPage();hydrateMetadata();applyWorkspaceLayout();updateApplyState();
}
async function restoreSavedDraft(){
 if(importPromise)await importPromise;
 if(savePromise)await savePromise;
 if(!await settleDialog())return;
 if(!confirm('Отбросить черновик и загрузить сохранённые настройки? Выполненные AI-запросы и история не отменяются.'))return;
 const revision=configRevision;const c=normalizeConfig(await api('/api/config'));if(configRevision!==revision){notify('Появились новые правки. Черновик не заменён.');return;}state.config=c;state.pipeline=c.active_pipeline_id;state.node=null;state.edge=null;configRevision++;markApplied(c);saved();renderPage();hydrateMetadata();applyWorkspaceLayout();
}
function acknowledgeProviderSave(p){
 if(appliedConfig){const index=appliedConfig.providers.findIndex(x=>x.id===p.id);if(index<0)appliedConfig.providers.push(copy(p));else appliedConfig.providers[index]=copy(p);appliedJSON=configDigest(appliedConfig);}
 // Provider fields are deliberately excluded from draft history.
 updateApplyState();refreshWorkspaceState(true).catch(failure);
}
function acknowledgeActivation(id){
 if(appliedConfig){appliedConfig.active_pipeline_id=id;appliedJSON=configDigest(appliedConfig);}
 updateApplyState();refreshWorkspaceState(true).catch(failure);
}
function workspaceSettings(){return state.config.control_center.workspace||={sidebar_collapsed:false,result_collapsed:false,inspector_width:300,show_fallbacks:false};}
function applyWorkspaceLayout(){
 if(!state.config)return;const w=workspaceSettings(),app=$('#app');app.classList.toggle('sidebar-collapsed',!!w.sidebar_collapsed);
 $('#toggleSidebar')?.setAttribute('aria-expanded',String(!w.sidebar_collapsed));
 const main=$('.main'),available=main?.clientWidth||900,width=clamp(w.inspector_width||300,260,Math.min(600,Math.max(260,available-260)));
 app.style.setProperty('--inspector',width+'px');
 applyResultLayout();
}
function applyResultLayout(){
 const box=$('#outputBox'),hasAnswer=!!state.job?.id&&!state.job?.cleared&&state.job?.purpose!=='connection_test';
 if(box){box.classList.toggle('result-idle',!hasAnswer);box.classList.toggle('collapsed',!!workspaceSettings().result_collapsed);}
 if($('#toggleResult')){$('#toggleResult').textContent=workspaceSettings().result_collapsed?'Развернуть ответ':'Свернуть ответ';$('#toggleResult').setAttribute('aria-expanded',String(!workspaceSettings().result_collapsed));}
}
function installInspectorResize(){
 const grip=$('#inspectorResize');if(!grip)return;let drag=null;
 grip.onpointerdown=e=>{e.preventDefault();drag={x:e.clientX,width:$('#inspector').getBoundingClientRect().width};grip.setPointerCapture(e.pointerId);};
 grip.onpointermove=e=>{if(!drag)return;workspaceSettings().inspector_width=clamp(Math.round(drag.width+drag.x-e.clientX),260,600);applyWorkspaceLayout();};
 const end=()=>{if(!drag)return;drag=null;dirty();drawLinks();};grip.onpointerup=end;grip.onpointercancel=end;
 grip.onkeydown=e=>{if(!['ArrowLeft','ArrowRight'].includes(e.key))return;e.preventDefault();workspaceSettings().inspector_width=clamp((workspaceSettings().inspector_width||300)+(e.key==='ArrowLeft'?16:-16),260,600);dirty();applyWorkspaceLayout();};
}
const settingTargets=[
 {label:'Ожидание первого текста',keywords:'первый токен ttft timeout таймаут',page:'pipelines',tab:'budget',field:'bFirst'},
 {label:'Бюджет вызовов',keywords:'лимит запросы ключи бюджет',page:'pipelines',tab:'budget',field:'bCalls'},
 {label:'Параллельность всего пайплайна',keywords:'одновременно concurrency',page:'pipelines',tab:'budget',field:'bParallel'},
 {label:'Порядок моделей и запуск резерва',keywords:'round robin race staggered hedge задержка',page:'pipelines',tab:'models',field:'poolOrder'},
 {label:'Источник моделей: список или каталог',keywords:'все модели fallback',page:'pipelines',tab:'models',field:'sourceMode'},
 {label:'Качество и фильтры моделей',keywords:'quality floor free порог',page:'pipelines',tab:'models',field:'sourceFloor'},
 {label:'Инструкции и итоговый prompt',keywords:'system user vision предыдущий',page:'pipelines',tab:'prompts',field:'systemPrompt'},
 {label:'Подключения и API-ключи',keywords:'provider opencode direct ключи',page:'providers',field:'providerSearch'},
 {label:'Локальное распознавание речи',keywords:'local stt whisper helper model weights распознавание аудио',page:'providers',field:'providerSearch'},
 {label:'AI VPN',keywords:'vless hysteria sing-box сеть туннель',page:'settings',field:'vpnEnabled'},
 {label:'Горячая клавиша снимка',keywords:'hotkey screenshot capture',page:'settings',field:'hotkey_analyze'},
 {label:'Показать / скрыть оверлей',keywords:'visibility hide overlay окно',page:'settings',field:'hotkey_visibility'},
 {label:'Источники, контекст и постоянный фон',keywords:'audio звук контекст разговор source background',page:'pipelines',field:'openRuntimeEditor'},
 {label:'Проверить захват без AI',keywords:'screenshot preview снимок',page:'settings',field:'settingsCapture'},
 {label:'Шрифт оверлея',keywords:'размер текст',page:'settings',field:'fontSize'},
 {label:'Ghost Mode',keywords:'click through клики мышь',page:'settings',field:'ghost'},
 {label:'Последний запуск и журнал',keywords:'trace error ошибка отмена ответ',page:'runs'},
];
async function openSettingSearch(){
 if($('#dialog').open&&!await closeModal())return;
 modal('Найти настройку','<input id="settingSearch" class="search-settings" type="search" placeholder="Например: первый токен, ключи, VPN" aria-label="Поиск настройки"><div id="settingResults"></div>','НАСТРОЙКИ · CTRL K');
 function rows(){const q=$('#settingSearch').value.toLowerCase().trim(),hits=settingTargets.filter(t=>(t.label+' '+t.keywords).toLowerCase().includes(q));$('#settingResults').innerHTML=hits.map(t=>`<button class="setting-hit" data-setting="${settingTargets.indexOf(t)}">${esc(t.label)}<small>${{pipelines:'Пайплайны',providers:'Провайдеры',settings:'Приложение',runs:'Запуски'}[t.page]}</small></button>`).join('')||'<p class="note">Настройка не найдена.</p>';$$('[data-setting]').forEach(el=>el.onclick=guarded(()=>openSettingTarget(settingTargets[Number(el.dataset.setting)])));}
 $('#settingSearch').oninput=rows;rows();$('#settingSearch').focus();
 $('#settingSearch').onkeydown=e=>{if(e.key==='Enter')$('#settingResults button')?.click();};
}
async function openSettingTarget(t){
 if($('#dialog').open&&!await closeModal())return;
 flushFocusedEditor();
 if(t.pipelineID&&state.config.pipelines.some(p=>p.id===t.pipelineID))state.pipeline=t.pipelineID;
 state.page=t.page||'pipelines';state.edge=null;
 if(state.page==='pipelines'){
  state.node=t.nodeID||selectedNode()?.id||pipe().nodes.find(n=>n.model_pool)?.id;state.tab=t.tab||'models';
  if(t.edgeID){if(!pipe().edges.some(e=>e.id===t.edgeID)){notify('Эта связь удалена из текущей конфигурации. История не изменена.');return;}state.edge=t.edgeID;}
 }
 renderPage();applyWorkspaceLayout();
 if(t.providerID){const p=provider(t.providerID);if(!p){notify('Подключение удалено из текущей конфигурации.');return;}providerDialog(p);return;}
 let el=t.field?$('#'+t.field):null;
 if(!el&&t.field==='sourceFloor'){state.tab='budget';renderInspector();el=$('#pQuality');}
 if(el){for(let p=el.parentElement;p;p=p.parentElement)if(p.tagName==='DETAILS')p.open=true;
  const target=el.hidden?(el.parentElement.querySelector('.inherit-toggle')||el.parentElement):el;
  target.scrollIntoView?.({block:'nearest'});target.focus();target.classList.add('setting-highlight');setTimeout(()=>target.classList.remove('setting-highlight'),1600);
 }
}
async function openAboutStudio(){
 if($('#dialog').open&&!await closeModal())return;
 const info=await refreshWorkspaceState(true);
 modal('AIOverlay',`<dl class="about-grid"><dt>Версия</dt><dd>${esc(info?.version||'—')}</dd><dt>Studio</dt><dd>${native()?'Встроенный WebView2':'HTTP · разработка'}</dd><dt>Основное приложение</dt><dd>Go · Win32 · Windows 10/11 x64</dd><dt>Сохранение</dt><dd>Переносимый config.json</dd></dl><p class="note">Закрытие Studio освобождает её сессию. Оверлей и активный пайплайн продолжают работать.</p>`,'О ПРОГРАММЕ');
}
