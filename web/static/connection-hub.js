'use strict';

function credentialBadge(providerID,keyID){
 const s=workspaceInfo?.credentials?.find(k=>k.provider_id===providerID&&k.credential_id===keyID);
 if(!s)return 'Не проверялся';if(!s.enabled)return 'Выключен';
 const left=Date.parse(s.cooldown_until)-Date.now();
 if(s.outcome==='auth_failed')return 'Ключ отклонён';
 if(left>0)return 'Пауза: ещё '+Math.ceil(left/1000)+' с';
 return {success:'Последний запрос успешен',rate_limited:'Лимит частоты · можно повторить',quota_exceeded:'Квота исчерпана',timeout:'Таймаут · ключ не заблокирован',first_token_timeout:'Первый текст не получен',network_error:'Сетевая ошибка',cancelled:'Запрос отменён'}[s.outcome]||(s.outcome?'Последний исход: '+(outcomeNames[s.outcome]||s.outcome):'Не проверялся');
}
function updateCredentialBadges(){
 $$('[data-key-status]').forEach(el=>el.textContent=credentialBadge(el.dataset.providerId,el.dataset.keyStatus));
}
function configuredRoutes(id){return state.config.providers.filter(p=>!['local_stt','openai_live'].includes(p.kind)&&catalogProviderID(p)===id);}
function renderProviders(){
 $('#page').innerHTML=`<div class="page-head"><h1>Подключения</h1><button class="button" id="addProvider">+ Подключение / Local STT</button></div><div class="connections-workspace"><div class="directory-toolbar"><input id="providerSearch" type="search" placeholder="Найти провайдера…" aria-label="Поиск провайдера"><label class="switch"><input type="checkbox" id="providersConfigured">Сохранённые</label><span id="directoryCount" class="dim"></span></div><div class="connection-tabs"><button id="allConnectionsTab" class="tab active">Все способы</button><button id="openCodeTab" class="tab">Via OpenCode</button><button id="directTab" class="tab">Direct</button></div><div class="directory-scroll"><h3 class="directory-section">Сохранённые подключения</h3><div id="savedConnections"></div><h3 class="directory-section">Добавить провайдера</h3><table class="provider-directory"><thead><tr><th>Провайдер</th><th>Моделей</th><th>Способ подключения</th><th></th></tr></thead><tbody id="providerDirectory"></tbody></table></div></div>`;
 let mode='all',directory=[];
 function rows(){
  if(!$('#providerDirectory'))return;const q=$('#providerSearch').value.trim().toLowerCase();
  const saved=(appliedConfig?.providers||state.config.providers).filter(p=>(p.label+' '+p.id+' '+catalogProviderID(p)).toLowerCase().includes(q)).filter(p=>mode==='all'||(mode==='opencode')===(p.kind==='embedded_opencode'));
  $('#savedConnections').innerHTML=saved.map(p=>{const local=p.kind==='local_stt',live=p.kind==='openai_live',stateText=!p.enabled?'Выключено':local?'локальный resource · без ключа':live?'persistent session · capabilities ниже':keyCount(p)?keyCount(p)+' ключей':catalogProviderID(p)==='opencode'?'FREE без ключа':'Нужен ключ';return `<div class="saved-connection"><div><strong>${esc(p.label)}</strong><small>${esc(routeLabel(p))} · ${esc(stateText)}</small></div><div class="tool-group"><button class="button small" data-edit-connection="${esc(p.id)}">${local?'Настроить':'Ключи'}</button>${local?'<span class="dim">Проверка — в источнике звука</span>':live?'<span class="dim">Проверка — через Live source</span>':`<button class="button small" data-test-connection="${esc(p.id)}">Проверить</button>`}</div></div>`;}).join('')||'<p class="note">Нет сохранённых подключений по этому фильтру.</p>';
  let found=directory.filter(d=>(d.name+' '+d.id).toLowerCase().includes(q));
  if($('#providersConfigured').checked)found=found.filter(d=>configuredRoutes(d.id).length);
  if(mode==='direct')found=found.filter(d=>configuredRoutes(d.id).some(p=>p.kind!=='embedded_opencode'));
  $('#directoryCount').textContent=found.length+' провайдеров';
  $('#providerDirectory').innerHTML=found.map(d=>{const routes=configuredRoutes(d.id),hasDirect=routes.some(p=>p.kind!=='embedded_opencode');return `<tr><td><strong>${esc(d.name||d.id)}</strong><small class="mono">${esc(d.id)}</small></td><td>${d.models||'—'}</td><td>${hasDirect?'Direct / ':''}OpenCode</td><td><div class="tool-group"><button class="button small" data-models-provider="${esc(d.id)}">Модели</button><button class="button small" data-connect-provider="${esc(d.id)}">${routes.length?'Настроить':'Подключить'}</button></div></td></tr>`;}).join('')||'<tr><td colspan="4">Провайдеры не найдены.</td></tr>';
  $$('[data-edit-connection]').forEach(b=>b.onclick=()=>providerDialog(provider(b.dataset.editConnection)));
  $$('[data-test-connection]').forEach(b=>b.onclick=guarded(()=>openConnectionProbe(b.dataset.testConnection)));
  $$('[data-connect-provider]').forEach(b=>b.onclick=guarded(()=>chooseProviderRoute(found.find(d=>d.id===b.dataset.connectProvider),mode)));
  $$('[data-models-provider]').forEach(b=>b.onclick=()=>{const pool=selectedNode()?.model_pool||pipe().nodes.find(n=>n.model_pool)?.model_pool;if(!pool){failure(new Error('Сначала добавьте этап моделей.'));return;}const id=b.dataset.modelsProvider,hasDirect=configuredRoutes(id).some(p=>p.kind!=='embedded_opencode');openCatalog(pool,false,{provider:id,transport:mode==='all'?(hasDirect?'all':'opencode'):mode});});
 }
 $('#providerSearch').oninput=rows;$('#providersConfigured').onchange=rows;
 for(const [id,value]of [['allConnectionsTab','all'],['openCodeTab','opencode'],['directTab','direct']])$('#'+id).onclick=()=>{mode=value;$$('.connection-tabs .tab').forEach(b=>b.classList.toggle('active',b.id===id));rows();};
 $('#addProvider').onclick=()=>providerDialog(providerTemplate(uid('provider'),'Свой API'));
 guarded(async()=>{directory=await providerDirectory();rows();})();
}
async function chooseProviderRoute(meta,mode='all'){
 const routes=configuredRoutes(meta.id).filter(p=>mode==='all'||(mode==='opencode')===(p.kind==='embedded_opencode'));
 if(routes.length===1){providerDialog(routes[0]);return;}
 if(!routes.length&&mode!=='direct'){providerDialog(newOpenCodeConnection(meta));return;}
 modal(meta.name||meta.id,`<p class="note">Способ вызова выбирается явно. Существующее подключение не будет заменено.</p>${routes.map(p=>`<button class="route-choice" data-existing-route="${esc(p.id)}"><strong>${esc(p.label)}</strong><small>${esc(routeLabel(p))} · ${esc(p.id)}</small></button>`).join('')}<button class="button" id="addOCConnection">+ Отдельное подключение через OpenCode</button>`,'СПОСОБ ПОДКЛЮЧЕНИЯ');
 $$('[data-existing-route]').forEach(b=>b.onclick=()=>providerDialog(provider(b.dataset.existingRoute)));
 $('#addOCConnection').onclick=()=>providerDialog(newOpenCodeConnection(meta));
}
async function openConnectionProbe(id){
 if($('#dialog').open&&!await closeModal())return;
 const p=appliedConfig?.providers.find(p=>p.id===id);if(!p||!p.enabled)throw new Error('Сначала сохраните и включите подключение.');
 const keys=(p.credential_pool.keys||[]).filter(k=>k.enabled&&k.secret),anonymous=p.kind==='embedded_opencode'&&catalogProviderID(p)==='opencode';
 if(!keys.length&&!anonymous)throw new Error('Добавьте и сохраните ключ перед проверкой.');
 const catalog=await api('/api/catalog?provider_id='+encodeURIComponent(catalogProviderID(p))+'&limit=500');
 const models=(catalog.items||[]).filter(m=>m.output_modalities?.includes('text')&&(keys.length||m.ready&&m.free));
 modal('Проверить подключение',`<div class="connection-identity">${tag(routeLabel(p))}<strong>${esc(p.label)}</strong></div>${selectField('Сохранённый ключ','probeKey',Object.fromEntries(keys.length?keys.map(k=>[k.id,(k.label||k.id)+' · •••• '+k.secret.slice(-4)]):[['keyless','FREE · без ключа']]),keys[0]?.id||'keyless')}${field('Model ID','probeModel',models.length?(p.kind==='embedded_opencode'?catalogProviderID(p)+'/':'')+models[0].model_id:'')}<datalist id="probeModels">${models.map(m=>`<option value="${esc((p.kind==='embedded_opencode'?catalogProviderID(p)+'/':'')+m.model_id)}">${esc(m.model_name)}</option>`).join('')}</datalist><p class="probe-warning">Один реальный запрос выбранной модели: «Reply only OK.», без screenshot, без повторов и fallback. До 32 токенов ответа. Возможен расход квоты и оплата по тарифу провайдера. Используется сохранённый сетевой маршрут приложения.</p><label class="switch"><input id="probeConsent" type="checkbox">Разрешаю этот один запрос</label><div class="dialog-footer"><button class="button primary" id="confirmProbe" disabled>Отправить тестовый запрос</button></div><div id="probeResult" aria-live="polite"></div>`,'РЕАЛЬНЫЙ AI-ЗАПРОС');
 $('#probeModel').setAttribute('list','probeModels');$('#probeConsent').onchange=e=>$('#confirmProbe').disabled=!e.target.checked;
 $('#confirmProbe').onclick=guarded(async()=>{
  const b=$('#confirmProbe');b.disabled=true;
  try{await api('/api/providers/test','POST',{provider_id:p.id,model_id:$('#probeModel').value.trim(),credential_id:$('#probeKey').value,confirm:$('#probeConsent').checked});await closeModal(true);state.job=await api('/api/job');state.page='runs';renderPage();}finally{if(b.isConnected)b.disabled=false;}
 });
}
function rememberCatalogModel(meta){
 const ref=meta.provider_id+'/'+meta.model_id,c=state.config.control_center;c.recent_models=[ref,...(c.recent_models||[]).filter(x=>x!==ref)].slice(0,30);
}
function favoriteModel(ref){return state.config.control_center.favorite_models.includes(ref);}
function toggleFavorite(ref){const c=state.config.control_center;c.favorite_models=favoriteModel(ref)?c.favorite_models.filter(x=>x!==ref):[...c.favorite_models,ref].slice(0,500);dirty();}
