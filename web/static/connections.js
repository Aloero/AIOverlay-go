'use strict';

// Provider discovery and connection setup. Catalog identity is not a transport.
let providerDirectoryCache;
async function providerDirectory() {
 if (!providerDirectoryCache) providerDirectoryCache = await api('/api/providers/catalog');
 return providerDirectoryCache;
}
function providerTemplate(id, label) {
 return {id,label,kind:'openai_compatible',enabled:true,base_url:'',headers:{},credential_pool:{policy:'round_robin',keys:[],max_keys_per_request:3,rate_limit_cooldown_s:60},request_defaults:{connect_timeout_ms:3000,first_token_timeout_ms:12000,response_timeout_ms:20000,stream_idle_timeout_ms:5000,max_output_tokens:2048}};
}
function catalogProviderID(p) {return p.kind==='embedded_opencode' ? (p.opencode_provider_id||'opencode') : p.catalog_provider_id || (p.kind==='gemini'?'google':p.id);}
function nativeConnection(m) {return state.config.providers.find(p=>p.kind!=='embedded_opencode' && catalogProviderID(p)===m.provider_id);}
function openCodeConnection(id) {return state.config.providers.find(p=>p.kind==='embedded_opencode' && catalogProviderID(p)===id);}
function keyCount(p) {return (p?.credential_pool?.keys||[]).filter(k=>k.enabled && k.secret?.trim()).length;}
function connectionFor(meta, route) {return route==='opencode' ? openCodeConnection(meta.provider_id) : nativeConnection(meta);}
function newOpenCodeConnection(meta) {
 const id=meta.provider_id||meta.id;
 const p=providerTemplate('oc_'+id.replace(/[^a-z\d_-]/gi,'_'),meta.provider_name||meta.name||id);
 p.kind='embedded_opencode';p.opencode_provider_id=id;p.credential_env=meta.credential_env||[];
 // Never reuse an unrelated custom configuration ID.
 if(provider(p.id))p.id=uid(p.id);
 return p;
}
function routeLabel(p) {return p?.kind==='embedded_opencode'?'Via OpenCode':'Direct';}

function renderProviders() {
 $('#page').innerHTML=`<div class="page-head"><h1>Провайдеры</h1><button class="button" id="addProvider" data-help="Добавить собственный OpenAI-compatible endpoint. Для провайдеров OpenCode выберите строку в каталоге ниже.">+ Свой API</button></div>
 <div class="connections-workspace"><div class="connection-tabs"><button class="tab active" id="openCodeTab">OpenCode</button><button class="tab" id="directTab">Direct</button></div><div class="directory-toolbar"><input id="providerSearch" type="search" placeholder="Найти провайдера…" aria-label="Поиск провайдера"><label class="switch"><input type="checkbox" id="providersConfigured">Настроенные</label><span id="directoryCount" class="dim"></span></div><div class="directory-scroll"><table class="provider-directory"><thead><tr><th>Провайдер</th><th>Моделей</th><th>Доступ</th><th></th></tr></thead><tbody id="providerDirectory"><tr><td colspan="4">Загрузка каталога…</td></tr></tbody></table></div></div>`;
 let mode='opencode', directory=[];
 function rows() {
  const root=$('#providerDirectory');if(!root)return;
  const search=$('#providerSearch').value.trim().toLowerCase();
  const all=mode==='opencode'?directory:state.config.providers.filter(p=>p.kind!=='embedded_opencode').map(p=>({id:catalogProviderID(p),name:p.label,config:p,models:directory.find(d=>d.id===catalogProviderID(p))?.models||0}));
  const filtered=all.filter(d=>(d.name+' '+d.id).toLowerCase().includes(search)).filter(d=>!$('#providersConfigured').checked || keyCount(d.config||openCodeConnection(d.id))>0 || d.id==='opencode');
  root.innerHTML=filtered.map(d=>{
   const p=d.config||openCodeConnection(d.id),keys=keyCount(p),free=d.id==='opencode'&&mode==='opencode';
   const status=p?.enabled ? (keys?`${keys} ${keys===1?'ключ':'ключей'}`:free?'Free без ключа':'Нужен ключ') : p?'Отключён':'Не настроен';
   return `<tr><td><strong>${esc(d.name||d.id)}</strong><small class="mono">${esc(d.id)}</small></td><td class="mono">${d.models||'—'}</td><td><span class="connection-status ${p?.enabled&&(keys||free)?'configured':''}" data-help="Настроен означает наличие подключения, не проверку баланса, региона или доступа к каждой модели. Результат реального запроса виден в Запусках.">${esc(status)}</span></td><td><div class="tool-group"><button class="button small" data-models-provider="${esc(d.id)}">Модели</button><button class="button small ${keys?'':'primary'}" data-connect-provider="${esc(d.id)}" ${p?`data-provider="${esc(p.id)}"`:''}>${keys||free?'Ключи / настройки':'Подключить'}</button></div></td></tr>`;
  }).join('')||'<tr><td colspan="4" class="dim">Провайдеры не найдены.</td></tr>';
  $('#directoryCount').textContent=filtered.length+' провайдеров';
  $$('[data-connect-provider]').forEach(b=>b.onclick=()=>{
   const d=filtered.find(d=>d.id===b.dataset.connectProvider);
   providerDialog(d.config||openCodeConnection(d.id)||newOpenCodeConnection(d));
  });
  $$('[data-models-provider]').forEach(b=>b.onclick=()=>{
   const pool=selectedNode()?.model_pool||pipe().nodes.find(n=>n.model_pool)?.model_pool;
   if(!pool){failure(new Error('Добавьте узел моделей в активный пайплайн.'));return;}
   openCatalog(pool,false,{provider:b.dataset.modelsProvider,transport:mode});
  });
 }
 $('#providerSearch').oninput=rows;$('#providersConfigured').onchange=rows;
 $('#openCodeTab').onclick=()=>{mode='opencode';$('#openCodeTab').classList.add('active');$('#directTab').classList.remove('active');rows();};
 $('#directTab').onclick=()=>{mode='direct';$('#directTab').classList.add('active');$('#openCodeTab').classList.remove('active');rows();};
 $('#addProvider').onclick=()=>providerDialog(providerTemplate(uid('provider'),'Свой API'));
 guarded(async()=>{directory=await providerDirectory();rows();})();
}

function providerDialog(connection, onDone) {
 const p=copy(connection);p.credential_pool.keys||=[];
 const via=p.kind==='embedded_opencode',cp=catalogProviderID(p);
 modal(p.label,`<div class="connection-identity">${tag(via?'Via OpenCode':'Direct')}<code>${esc(cp)}</code></div>
 <div class="connection-form"><section><h3>API-ключи</h3><div id="providerKeys"></div><div class="field"><label for="bulkKeys">Добавить ключи</label><textarea id="bulkKeys" class="secret-entry" rows="3" spellcheck="false" autocomplete="off" placeholder="Вставьте один или несколько ключей" data-help="Один ключ на строку; буквальная последовательность \\n тоже поддерживается. Повторы не добавляются. После добавления секрет скрыт."></textarea></div><button class="button" id="addKeys">Добавить</button><p class="secret-notice">Ключи хранятся в config.json в открытом виде.</p>${via&&cp==='opencode'?'<p class="note">Для доступных Free-моделей OpenCode ключ не нужен. Платным моделям нужен ключ OpenCode.</p>':''}</section>
 <section><h3>Использование</h3><label class="switch"><input type="checkbox" id="providerEnabled" ${p.enabled?'checked':''}>Подключение включено</label>${selectField('Выбор ключа','keyPolicy',keyPolicyNames,p.credential_pool.policy)}<div class="form-row">${field('Ключей на модель','keyMax',p.credential_pool.max_keys_per_request,'number')}${field('Пауза после 429, с','keyCooldown',p.credential_pool.rate_limit_cooldown_s,'number')}</div>${field('Макс. токенов ответа','providerTokens',p.request_defaults.max_output_tokens,'number')}</section></div>
 <details class="advanced-connection"><summary>Параметры подключения</summary>${field('Название','providerLabel',p.label)}${via?`<div class="field"><label>Provider ID</label><code>${esc(cp)}</code></div>${field('Переменная ключа','credentialEnv',(p.credential_env||[]).join(', '),'text','Определяется по Models.dev. OpenCode также получает apiKey в изолированной конфигурации текущего провайдера.')}`:selectField('Протокол','providerKind',{openai_compatible:'OpenAI-compatible API',gemini:'Gemini API'},p.kind)}${field(via?'Base URL (необязательно)':'Base URL','providerURL',p.base_url||'','url')}${!via?field('Provider ID в Models.dev','catalogProviderID',p.catalog_provider_id||catalogProviderID(p)):''}</details>
 <div class="dialog-footer"><span class="dim">${via&&!(p.credential_env||[]).length&&cp!=='opencode'?'OAuth и облачная авторизация не заменяются обычным API-ключом.':'Авторизация проверяется при запросе.'}</span><button class="button primary" id="providerDone">Сохранить подключение</button></div>`,via?'OPENCODE / ПОДКЛЮЧЕНИЕ':'DIRECT / ПОДКЛЮЧЕНИЕ');
 function keys() {
  $('#providerKeys').innerHTML=p.credential_pool.keys.map((k,i)=>`<div class="key-row"><input type="checkbox" data-key-enable="${i}" ${k.enabled?'checked':''} aria-label="Использовать ключ ${i+1}"><input class="key-name" data-key-name="${i}" value="${esc(k.label||k.id)}" aria-label="Название ключа"><span class="mono">•••• ${esc(k.secret?.length>4?k.secret.slice(-4):'')}</span><button class="icon-button" data-key-remove="${i}" aria-label="Удалить ключ">×</button></div>`).join('')||'<div class="key-empty">Нет сохранённых ключей</div>';
  $$('[data-key-enable]').forEach(e=>e.onchange=()=>{p.credential_pool.keys[Number(e.dataset.keyEnable)].enabled=e.checked;});
  $$('[data-key-name]').forEach(e=>e.onchange=()=>{p.credential_pool.keys[Number(e.dataset.keyName)].label=e.value;});
  $$('[data-key-remove]').forEach(e=>e.onclick=()=>{p.credential_pool.keys.splice(Number(e.dataset.keyRemove),1);keys();});
 }
 // Connection edits are isolated from the graph draft until the endpoint accepts them.
 function bind(id,obj,key,numeric=false) {const el=$('#'+id);if(el)el.onchange=()=>{obj[key]=numeric?finite(el.value):el.value;};}
 bind('providerLabel',p,'label');bind('providerURL',p,'base_url');bind('providerKind',p,'kind');bind('catalogProviderID',p,'catalog_provider_id');
 bind('keyPolicy',p.credential_pool,'policy');bind('keyMax',p.credential_pool,'max_keys_per_request',true);bind('keyCooldown',p.credential_pool,'rate_limit_cooldown_s',true);bind('providerTokens',p.request_defaults,'max_output_tokens',true);
 if($('#credentialEnv'))$('#credentialEnv').onchange=e=>{p.credential_env=e.target.value.split(',').map(v=>v.trim()).filter(Boolean);};
 $('#providerEnabled').onchange=e=>{p.enabled=e.target.checked;};
 function addKeys() {
  const existing=new Set(p.credential_pool.keys.map(k=>k.secret));let count=0;
  for(const key of $('#bulkKeys').value.replace(/\\n/g,'\n').split(/[\r\n]+/).map(v=>v.trim()).filter(Boolean)) {
   if(existing.has(key))continue;existing.add(key);count++;
   p.credential_pool.keys.push({id:uid('key'),label:'Ключ '+(p.credential_pool.keys.length+1),secret:key,enabled:true,priority:100});
  }
  $('#bulkKeys').value='';keys();return count;
 }
 $('#addKeys').onclick=()=>{const count=addKeys();notify(count?'Добавлено ключей: '+count:'Новых ключей нет.');};
 $('#providerDone').onclick=guarded(async()=>{
  addKeys();const button=$('#providerDone');button.disabled=true;
  if(savePromise)await savePromise;
  try {
   await api('/api/providers/config','POST',p);
   const index=state.config.providers.findIndex(v=>v.id===p.id);
   if(index<0)state.config.providers.push(p);else state.config.providers[index]=p;
   closeModal();if(onDone)await onDone(p);else renderPage();
   notify('Подключение сохранено.');
  }finally{if(button.isConnected)button.disabled=false;}
 });
 keys();$('#bulkKeys').focus();
}

async function selectCatalogModel(pool,meta,route,visionOnly) {
 if(!pool)throw new Error('Не выбран узел моделей.');
 const append=async p=>{
  const id=p.kind==='embedded_opencode'?meta.provider_id+'/'+meta.model_id:meta.model_id;
  if(pool.models.some(s=>s.provider_id===p.id&&s.model_id===id)){notify('Модель уже в списке.');return;}
  pool.models.push({id:uid('model'),provider_id:p.id,model_id:id,enabled:true,priority:100-pool.models.length,weight:1,quality_score:80,timeout_ms:20000,first_token_timeout_ms:12000,max_attempts:1});
  if(!visionOnly&&!meta.input_modalities.includes('image')&&!pool.vision_preprocessor) {
   pool.vision_preprocessor=defaultVisionPool();notify('Перед текстовой моделью добавлен отдельный этап зрения.');
  }
  dirty();closeModal();state.page='pipelines';renderPage();
 };
 let p=connectionFor(meta,route);
 if(!p && route==='opencode')p=newOpenCodeConnection(meta);
 if(!p)throw new Error('Сначала настройте Direct-подключение к этому провайдеру.');
 const anonymous=p.kind==='embedded_opencode'&&catalogProviderID(p)==='opencode'&&meta.free&&meta.ready;
 if(!p.enabled || (!keyCount(p)&&!anonymous)) {providerDialog(p,append);return;}
 await append(p);
}
