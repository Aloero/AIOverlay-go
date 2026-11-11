'use strict';

// Provider discovery and connection setup. Catalog identity is not a transport.
let providerDirectoryCache,providerCapabilitiesCache;
async function providerDirectory() {
 if (!providerDirectoryCache) providerDirectoryCache = await api('/api/providers/catalog');
 return providerDirectoryCache;
}
async function providerCapabilities() {
 if (!providerCapabilitiesCache) providerCapabilitiesCache = await api('/api/providers/capabilities');
 return providerCapabilitiesCache;
}
function providerTemplate(id, label) {
 return {id,label,kind:'openai_compatible',audio_input:false,enabled:true,base_url:'',headers:{},credential_pool:{policy:'round_robin',keys:[],max_keys_per_request:3,rate_limit_cooldown_s:60},request_defaults:{connect_timeout_ms:3000,first_token_timeout_ms:12000,response_timeout_ms:20000,stream_idle_timeout_ms:5000,max_output_tokens:2048}};
}
function catalogProviderID(p) {return p.kind==='embedded_opencode' ? (p.opencode_provider_id||'opencode') : p.catalog_provider_id || (p.kind==='gemini'?'google':p.id);}
function nativeConnection(m) {return state.config.providers.find(p=>!['embedded_opencode','embedded_hermes','local_stt','openai_live'].includes(p.kind) && catalogProviderID(p)===m.provider_id);}
function openCodeConnection(id) {return state.config.providers.find(p=>p.kind==='embedded_opencode' && catalogProviderID(p)===id);}
function hermesConnection(id) {return state.config.providers.find(p=>p.kind==='embedded_hermes' && catalogProviderID(p)===id);}
function keyCount(p) {return (p?.credential_pool?.keys||[]).filter(k=>k.enabled && k.secret?.trim()).length;}
function connectionFor(meta, route) {return route==='opencode' ? openCodeConnection(meta.provider_id) : route==='hermes' ? hermesConnection(meta.provider_id) : nativeConnection(meta);}
function newOpenCodeConnection(meta) {
 const id=meta.provider_id||meta.id;
 const p=providerTemplate('oc_'+id.replace(/[^a-z\d_-]/gi,'_'),meta.provider_name||meta.name||id);
 p.kind='embedded_opencode';p.opencode_provider_id=id;p.credential_env=meta.credential_env||[];
 // Never reuse an unrelated custom configuration ID.
 if(provider(p.id))p.id=uid(p.id);
 return p;
}
function routeLabel(p) {return p?.kind==='embedded_opencode'?'Via OpenCode':p?.kind==='embedded_hermes'?'Via Hermes':p?.kind==='openai_live'?'OpenAI Live':p?.kind==='local_stt'?'Local STT':'Direct';}
function connectionSupportsAudio(p){return !!p&&(p.kind==='local_stt'||p.kind==='openai_live'||p.kind==='gemini'||p.kind==='audio_transcription'||(p.kind==='openai_compatible'&&p.audio_input===true));}

const realtimeOperationNames={append_audio:'Передавать audio в Live session',update_image:'Обновлять image',request_response:'Запрашивать ответ',semantic_no_action:'Semantic no_action',text_output:'Получать text output',interrupt:'Прерывать ответ',clean_session:'Чистая session / isolation',reconnect:'Reconnect / resume',vpn_fail_closed:'AI VPN fail-closed'};
function realtimeCapabilityRows(capabilities){
 const operations=capabilities?.realtime?.operations||{};
 return Object.entries(realtimeOperationNames).map(([operation,label])=>`<div class="capability-row"><span>${esc(label)}</span><b>${operations[operation]?'SUPPORTED':'UNSUPPORTED'}</b></div>`).join('');
}
async function renderProviderCapabilities(connection){
 const root=$('#providerCapabilities');if(!root)return;
 try{
  const all=await providerCapabilities(),saved=all.find(item=>item.provider_id===connection.id);
  if(!saved){root.innerHTML='<p class="note">Матрица transport появится после сохранения подключения.</p>';return;}
  const media=[saved.media?.image?'IMAGE':'',saved.media?.audio?'AUDIO':'',saved.media?.transcription_only?'TRANSCRIPTION ONLY':''].filter(Boolean);
  if(saved.kind==='local_stt'){const local=saved.local_stt||{};const size=local.model_bytes?`${(local.model_bytes/1048576).toFixed(1)} MiB`:'—',policy=local.resource_policy==='unload_on_pause'?'выгружать на паузе':'держать warm';root.innerHTML=`<div class="tags">${media.map(value=>tag(value,'image')).join('')}</div><div class="capability-row"><span>Helper</span><b>${local.helper_present?'READY':'MISSING'}</b></div><div class="capability-row"><span>Model · ${esc(local.model_id||'не задан')}</span><b>${local.model_present?'READY · '+size:'MISSING'}</b></div><div class="capability-row"><span>Resource lifecycle · ${esc(policy)}</span><b>${local.warm?'WARM':'COLD'}${local.active_requests?' · active '+local.active_requests:''}${local.background_paused?' · PAUSED':''}</b></div>${local.reason?`<p class="note">${esc(local.reason)}</p>`:'<p class="note">Capabilities не запускают helper. unload_on_pause ждёт завершения уже принятой transcription и не обрывает активный request.</p>'}`;return;}
  root.innerHTML=`<div class="tags">${media.map(value=>tag(value,'image')).join('')||tag('TEXT','text')}</div><p class="note">Persistent realtime: <b>${saved.realtime?.available?'AVAILABLE':'UNSUPPORTED'}</b>. HTTP audio и streaming ответа не считаются realtime session.</p>${realtimeCapabilityRows(saved)}${saved.realtime?.reason?`<p class="note">${esc(saved.realtime.reason)}</p>`:''}`;
 }catch(error){root.innerHTML=`<p class="note">Матрица transport недоступна: ${esc(error.message||String(error))}</p>`;}
}

function providerDialog(connection, onDone) {
 const p=copy(connection);p.credential_pool.keys||=[];let committed=JSON.stringify(p);
 const via=p.kind==='embedded_opencode',local=p.kind==='local_stt',cp=catalogProviderID(p);
 modal(p.label,`<div class="connection-identity"><span id="providerRouteKind">${tag(routeLabel(p))}</span><code id="providerRouteIdentity">${esc(local?(p.local_stt?.model_id||p.id):cp)}</code></div>
 <div class="connection-form"><section id="providerKeySection" ${local?'hidden':''}><h3>API-ключи</h3><div id="providerKeys"></div><div class="field"><label for="bulkKeys">Добавить ключи</label><textarea id="bulkKeys" class="secret-entry" rows="3" spellcheck="false" autocomplete="off" placeholder="Вставьте один или несколько ключей" data-help="Один ключ на строку; буквальная последовательность \\n тоже поддерживается. Повторы не добавляются. После добавления секрет скрыт."></textarea></div><button class="button" id="addKeys">Добавить</button><p class="secret-notice">Ключи хранятся в config.json в открытом виде.</p>${via&&cp==='opencode'?'<p class="note">Для доступных Free-моделей OpenCode ключ не нужен. Платным моделям нужен ключ OpenCode.</p>':''}</section>
 <section><h3>Использование</h3><label class="switch"><input type="checkbox" id="providerEnabled" ${p.enabled?'checked':''}>Подключение включено</label><div id="providerCredentialPolicy" ${local?'hidden':''}>${selectField('Выбор ключа','keyPolicy',keyPolicyNames,p.credential_pool.policy)}<div class="form-row">${field('Ключей на модель','keyMax',p.credential_pool.max_keys_per_request,'number')}${field('Пауза после 429, с','keyCooldown',p.credential_pool.rate_limit_cooldown_s,'number')}</div>${field('Макс. токенов ответа','providerTokens',p.request_defaults.max_output_tokens,'number')}</div><h3>Transport capabilities</h3><div id="providerCapabilities"><p class="note">Проверяю локальный adapter contract…</p></div></section></div>
 <details class="advanced-connection"><summary>Параметры подключения</summary>${field('Название','providerLabel',p.label)}${via?`<div class="field"><label>Provider ID</label><code>${esc(cp)}</code></div>${field('Переменная ключа','credentialEnv',(p.credential_env||[]).join(', '),'text','Определяется по Models.dev. OpenCode также получает apiKey в изолированной конфигурации текущего провайдера.')}`:selectField('Протокол','providerKind',{openai_compatible:'OpenAI-compatible Chat API',audio_transcription:'Audio transcription API',gemini:'Gemini API',embedded_hermes:'Hermes · внешний харнес (нужен base URL провайдера)',openai_live:'OpenAI Live · persistent WebSocket',local_stt:'Local STT · helper IPC'},p.kind)+`<div id="providerChatAudio" ${p.kind==='openai_compatible'?'':'hidden'}><label class="switch"><input type="checkbox" id="providerAudioInput" ${p.audio_input?'checked':''}>Этот OpenAI-compatible Chat endpoint принимает raw AUDIO</label><small>Включайте только если endpoint действительно поддерживает audio content. Отдельный transcription API выбирайте протоколом выше.</small></div><div id="providerLiveFields" ${p.kind==='openai_live'?'':'hidden'}>${field('Responses delegation model','liveResponsesModel',p.realtime?.responses_model||'','text','Пусто = только persistent Live input. Запрос текста через response.create будет UNSUPPORTED.')}${field('Live instructions','liveInstructions',p.realtime?.instructions||'','text','Инструкции постоянной Live session. Remote memory не равна локальному Context.') }<label class="switch"><input type="checkbox" id="liveStore" ${p.realtime?.store?'checked':''}>Разрешить remote storage для WebSocket reconnect</label><small>Включайте только осознанно: stored Live session хранится у провайдера по его data policy. При разрыве AIOverlay делает fork сохранённой session и не replay-ит неоднозначный последний event.</small><p class="note">Live endpoint задаётся как credential-free wss:// URL. IMAGE, semantic no_action и interrupt остаются UNSUPPORTED. Reconnect доступен только при включённом storage.</p></div><div id="providerLocalSTTFields" ${p.kind==='local_stt'?'':'hidden'}>${field('Helper executable','localSTTHelper',p.local_stt?.helper_path||'runtime/stt/aio-stt-helper.exe','text','Абсолютный путь или путь относительно AIOverlay.exe. Helper должен говорить по aio-stt-v1 IPC.')}${field('Model resource','localSTTModelPath',p.local_stt?.model_path||'runtime/stt/models/ggml-small.bin','text','Weight/resource не скачивается автоматически и не входит в минимальный ZIP.')}${field('Model ID','localSTTModelID',p.local_stt?.model_id||'whisper-local','text','Это ID локального resource для model slot, не Models.dev.') }${selectField('Resource lifecycle','localSTTResourcePolicy',{keep_warm:'Держать helper/model warm',unload_on_pause:'Выгружать после Pause background'},p.local_stt?.resource_policy||'keep_warm')}<small>unload_on_pause не убивает уже выполняющийся request; выгрузка происходит после последнего active request. Resume до его завершения отменяет pending unload.</small><div class="field"><label for="localSTTArgs">Helper args · по одному на строку</label><textarea id="localSTTArgs" rows="3" spellcheck="false">${esc((p.local_stt?.args||[]).join('\n'))}</textarea></div><p class="note">PCM16 передаётся только через stdin/stdout IPC. Temp WAV/JPEG и локальный HTTP listener не используются.</p></div>`}<div id="providerURLBlock" ${local?'hidden':''}>${field(via?'Base URL (необязательно)':p.kind==='openai_live'?'Live WebSocket URL':'Base URL','providerURL',p.base_url||'','url')}</div><div id="providerCatalogBlock" ${via||local||p.kind==='openai_live'?'hidden':''}>${field('Provider ID в Models.dev','catalogProviderID',p.catalog_provider_id||catalogProviderID(p))}</div></details>
 <div class="dialog-footer"><span class="dim">${local?'Локальный provider не использует API-ключ и сеть.':via&&!(p.credential_env||[]).length&&cp!=='opencode'?'OAuth и облачная авторизация не заменяются обычным API-ключом.':'Авторизация проверяется при запросе.'}</span><button class="button primary" id="providerDone">Сохранить подключение</button></div>`,via?'OPENCODE / ПОДКЛЮЧЕНИЕ':'DIRECT / ПОДКЛЮЧЕНИЕ');
 function keys() {
  $('#providerKeys').innerHTML=p.credential_pool.keys.map((k,i)=>`<div class="key-row"><input type="checkbox" data-key-enable="${i}" ${k.enabled?'checked':''} aria-label="Использовать ключ ${i+1}"><input class="key-name" data-key-name="${i}" value="${esc(k.label||k.id)}" aria-label="Название ключа"><span class="mono">•••• ${esc(k.secret?.length>4?k.secret.slice(-4):'')}</span><span class="key-health" data-provider-id="${esc(p.id)}" data-key-status="${esc(k.id)}">${esc(credentialBadge(p.id,k.id))}</span><button class="icon-button" data-key-remove="${i}" aria-label="Удалить ключ">×</button></div>`).join('')||'<div class="key-empty">Нет сохранённых ключей</div>';
  $$('[data-key-enable]').forEach(e=>e.onchange=()=>{p.credential_pool.keys[Number(e.dataset.keyEnable)].enabled=e.checked;});
  $$('[data-key-name]').forEach(e=>e.onchange=()=>{p.credential_pool.keys[Number(e.dataset.keyName)].label=e.value;});
  $$('[data-key-remove]').forEach(e=>e.onclick=()=>{p.credential_pool.keys.splice(Number(e.dataset.keyRemove),1);keys();});
 }
 // Connection edits are isolated from the graph draft until the endpoint accepts them.
 function bind(id,obj,key,numeric=false) {const el=$('#'+id);if(el)el.onchange=()=>{obj[key]=numeric?finite(el.value):el.value;};}
 bind('providerLabel',p,'label');bind('providerURL',p,'base_url');if($('#providerKind'))$('#providerKind').onchange=e=>{p.kind=e.target.value;if(p.kind==='openai_live'){p.audio_input=false;p.realtime||={responses_model:'',instructions:''};delete p.local_stt;}else if(p.kind==='local_stt'){p.audio_input=false;delete p.realtime;p.base_url='';p.models_url='';p.catalog_provider_id='';p.headers={};p.credential_env=[];p.credential_pool.keys=[];p.local_stt||={helper_path:'runtime/stt/aio-stt-helper.exe',model_path:'runtime/stt/models/ggml-small.bin',model_id:'whisper-local',args:[],resource_policy:'keep_warm'};}else{delete p.realtime;delete p.local_stt;if(p.kind==='embedded_hermes')p.audio_input=false;}const localNow=p.kind==='local_stt';if($('#providerRouteKind'))$('#providerRouteKind').innerHTML=tag(routeLabel(p));if($('#providerRouteIdentity'))$('#providerRouteIdentity').textContent=localNow?(p.local_stt?.model_id||p.id):catalogProviderID(p);if($('#dialogEyebrow'))$('#dialogEyebrow').textContent=localNow?'LOCAL STT / РЕСУРС':p.kind==='openai_live'?'LIVE / PERSISTENT SESSION':p.kind==='embedded_hermes'?'HERMES / ХАРНЕС':'DIRECT / ПОДКЛЮЧЕНИЕ';$('#providerChatAudio')?.toggleAttribute('hidden',p.kind!=='openai_compatible');$('#providerLiveFields')?.toggleAttribute('hidden',p.kind!=='openai_live');$('#providerLocalSTTFields')?.toggleAttribute('hidden',!localNow);$('#providerKeySection')?.toggleAttribute('hidden',localNow);$('#providerCredentialPolicy')?.toggleAttribute('hidden',localNow);$('#providerURLBlock')?.toggleAttribute('hidden',localNow);$('#providerCatalogBlock')?.toggleAttribute('hidden',localNow||p.kind==='openai_live');};if($('#providerAudioInput'))$('#providerAudioInput').onchange=e=>{p.audio_input=e.target.checked;};bind('catalogProviderID',p,'catalog_provider_id');if($('#liveResponsesModel'))$('#liveResponsesModel').onchange=e=>{p.realtime||={};p.realtime.responses_model=e.target.value.trim();};if($('#liveInstructions'))$('#liveInstructions').onchange=e=>{p.realtime||={};p.realtime.instructions=e.target.value;};if($('#liveStore'))$('#liveStore').onchange=e=>{p.realtime||={};p.realtime.store=e.target.checked;};if($('#localSTTHelper'))$('#localSTTHelper').onchange=e=>{p.local_stt||={args:[]};p.local_stt.helper_path=e.target.value.trim();};if($('#localSTTModelPath'))$('#localSTTModelPath').onchange=e=>{p.local_stt||={args:[]};p.local_stt.model_path=e.target.value.trim();};if($('#localSTTModelID'))$('#localSTTModelID').onchange=e=>{p.local_stt||={args:[]};p.local_stt.model_id=e.target.value.trim();};if($('#localSTTResourcePolicy'))$('#localSTTResourcePolicy').onchange=e=>{p.local_stt||={args:[]};p.local_stt.resource_policy=e.target.value;};if($('#localSTTArgs'))$('#localSTTArgs').onchange=e=>{p.local_stt||={};p.local_stt.args=e.target.value.split(/\r?\n/).map(v=>v.trim()).filter(Boolean);};
 bind('keyPolicy',p.credential_pool,'policy');bind('keyMax',p.credential_pool,'max_keys_per_request',true);bind('keyCooldown',p.credential_pool,'rate_limit_cooldown_s',true);bind('providerTokens',p.request_defaults,'max_output_tokens',true);
 if($('#credentialEnv'))$('#credentialEnv').onchange=e=>{p.credential_env=e.target.value.split(',').map(v=>v.trim()).filter(Boolean);};
 $('#providerEnabled').onchange=e=>{p.enabled=e.target.checked;};
 renderProviderCapabilities(p);
 function addKeys() {
  const existing=new Set(p.credential_pool.keys.map(k=>k.secret));let count=0;
  for(const key of $('#bulkKeys').value.replace(/\\n/g,'\n').split(/[\r\n]+/).map(v=>v.trim()).filter(Boolean)) {
   if(existing.has(key))continue;existing.add(key);count++;
   p.credential_pool.keys.push({id:uid('key'),label:'Ключ '+(p.credential_pool.keys.length+1),secret:key,enabled:true,priority:100});
  }
  $('#bulkKeys').value='';keys();return count;
 }
 $('#addKeys').onclick=()=>{const count=addKeys();notify(count?'Добавлено ключей: '+count:'Новых ключей нет.');};
 const draft={
  saving:null,
  isDirty:()=>JSON.stringify(p)!==committed||!!$('#bulkKeys')?.value.trim(),
  discard:()=>{committed=JSON.stringify(p);if($('#bulkKeys'))$('#bulkKeys').value='';},
  save:async()=>{
   if(importPromise)await importPromise;
   if(draft.saving)return draft.saving;
   addKeys();const button=$('#providerDone');button.disabled=true;
   // Capture values before yielding. Payload mutation cannot change an in-flight request.
   const snapshot=copy(p),revision=JSON.stringify(snapshot);
   const prior=savePromise;
   draft.saving=(async()=>{
    if(prior)await prior;
    await api('/api/providers/config','POST',snapshot);
    providerCapabilitiesCache=null;
    const index=state.config.providers.findIndex(v=>v.id===snapshot.id);
    if(index<0)state.config.providers.push(snapshot);else state.config.providers[index]=snapshot;
    acknowledgeProviderSave(snapshot);
    configRevision++; // a concurrent graph save cannot mark a stale provider snapshot current.
    committed=revision;
    if(draft.isDirty()){notify('Подключение сохранено. Более новые правки остаются в диалоге.');return;}
    if(dialogDraft===draft)dialogDraft=null;
    await closeModal(true);if(onDone)await onDone(snapshot);else renderPage();
    notify('Подключение сохранено.');
   })();
   try{return await draft.saving;}finally{draft.saving=null;if(button.isConnected)button.disabled=false;}
  }
 };
 dialogDraft=draft;
 $('#providerDone').onclick=guarded(draft.save);
 keys();$('#bulkKeys').focus();
}

async function selectCatalogModel(pool,meta,route,visionOnly,connectionID) {
 if(!pool)throw new Error('Не выбран узел моделей.');
 const append=async p=>{
  const id=(p.kind==='embedded_opencode'||p.kind==='embedded_hermes')?meta.provider_id+'/'+meta.model_id:meta.model_id;
  if(pool.models.some(s=>s.provider_id===p.id&&s.model_id===id)){notify('Модель уже в списке.');return;}
  pool.models.push({id:uid('model'),provider_id:p.id,model_id:id,enabled:true,priority:100-pool.models.length,weight:1,quality_score:80,timeout_ms:0,first_token_timeout_ms:0,max_attempts:1});
  const audioRoute=pool.task==='transcribe'||['audio','audio_image'].includes(pool.selection?.modality);
  if(!visionOnly&&!audioRoute&&!meta.input_modalities.includes('image')&&!pool.vision_preprocessor) {
   pool.vision_preprocessor=defaultVisionPool();notify('Перед текстовой моделью добавлен отдельный этап зрения.');
  }
  rememberCatalogModel(meta);dirty();closeModal();state.page='pipelines';renderPage();
 };
 if(!connectionID){
  const kindForRoute=r=>r==='opencode'?'embedded_opencode':r==='hermes'?'embedded_hermes':'';
  const options=state.config.providers.filter(p=>catalogProviderID(p)===meta.provider_id&&(kindForRoute(route)?p.kind===kindForRoute(route):!['embedded_opencode','embedded_hermes','local_stt','openai_live'].includes(p.kind)));
  if(options.length>1){modal('Выбрать подключение',options.map(p=>`<button class="route-choice" data-select-connection="${esc(p.id)}">${esc(p.label)}<small>${esc(routeLabel(p))} · ${esc(p.id)}</small></button>`).join(''));$$('[data-select-connection]').forEach(b=>b.onclick=guarded(()=>selectCatalogModel(pool,meta,route,visionOnly,b.dataset.selectConnection)));return;}
 }
 let p=connectionID?provider(connectionID):connectionFor(meta,route);
 if(!p && route==='opencode')p=newOpenCodeConnection(meta);
 if(!p)throw new Error('Сначала настройте Direct-подключение к этому провайдеру.');
 const anonymous=p.kind==='embedded_opencode'&&catalogProviderID(p)==='opencode'&&meta.free&&meta.ready;
 if(!p.enabled || (!keyCount(p)&&!anonymous)) {providerDialog(p,append);return;}
 await append(p);
}
