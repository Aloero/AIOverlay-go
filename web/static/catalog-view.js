'use strict';
function renderLocalSTTCandidates(pool,query,capabilities){
 const root=$('#localSTTCandidates');if(!root)return;
 if(!query.audio||query.reasoning){root.innerHTML='';return;}
 const byID=new Map((capabilities||[]).map(item=>[item.provider_id,item.local_stt]));
 const needle=(query.provider||'').trim().toLowerCase();
 const locals=(state.config.providers||[]).filter(p=>p.enabled&&p.kind==='local_stt'&&p.local_stt).filter(p=>!needle||(p.id+' '+p.label+' '+p.local_stt.model_id).toLowerCase().includes(needle)).filter(p=>{const status=byID.get(p.id);return !query.ready||(status?.helper_present&&status?.model_present);});
 if(!locals.length){root.innerHTML='';return;}
 root.innerHTML=`<section class="vision-section"><p class="eyebrow">LOCAL STT · без Models.dev</p>${locals.map(p=>{const status=byID.get(p.id)||{},ready=status.helper_present&&status.model_present,size=status.model_bytes?` · ${(status.model_bytes/1048576).toFixed(1)} MiB`:'';return `<div class="route-choice"><div><strong>${esc(p.local_stt.model_id)}</strong><small>${esc(p.label||p.id)} · helper ${status.helper_present?'ready':'missing'} · model ${status.model_present?'ready':'missing'}${size}</small></div><button class="button small" data-select-local-stt="${esc(p.id)}" ${ready?'':'disabled'}>Добавить LOCAL</button></div>`;}).join('')}</section>`;
}
function selectLocalSTTModel(pool,providerID){
 const p=provider(providerID);if(!p?.local_stt)return;const modelID=p.local_stt.model_id;if((pool.models||[]).some(slot=>slot.provider_id===providerID&&slot.model_id===modelID)){notify('Локальная STT-модель уже в списке.');return;}
 pool.models||=[];pool.models.push({id:uid('model'),provider_id:providerID,model_id:modelID,enabled:true,priority:100-pool.models.length,weight:1,quality_score:80,timeout_ms:0,first_token_timeout_ms:0,max_attempts:1});
 pool.selection||={mode:'manual',provider_ids:[],free_only:false,modality:'audio',default_quality_score:0};if(pool.selection.mode==='catalog')pool.selection.mode='manual_then_catalog';pool.selection.modality='audio';dirty();closeModal();state.page='pipelines';renderPage();
}

function openCatalog(pool,visionOnly=false,context={}){
 modal(visionOnly?'Модели для извлечения задачи':'Каталог моделей',`<div class="catalog-controls"><input id="catSearch" placeholder="Модель, семейство, provider/model…" aria-label="Поиск модели"><input class="provider-search" id="catProvider" placeholder="Провайдер" aria-label="Фильтр провайдера"><button id="catRefresh" class="button">Обновить</button><button id="catWhy" class="button" aria-expanded="false">Почему не готово</button></div><div class="catalog-shortcuts"><button class="tab active" data-catalog-group="all">Все</button><button class="tab" data-catalog-group="favorites">Избранные</button><button class="tab" data-catalog-group="recent">Недавние</button></div><div class="catalog-filters"><label><input id="catVision" type="checkbox" ${visionOnly?'checked disabled':''}> IMAGE</label><label><input id="catAudio" type="checkbox" ${context.modality==='audio'||context.modality==='audio_image'||pool.task==='transcribe'?'checked disabled':''}> AUDIO</label><label><input id="catFree" type="checkbox"> FREE</label><label><input id="catReasoning" type="checkbox"> REASONING</label><label><input id="catReady" type="checkbox"> Настроен</label></div><div id="catWhyPanel" hidden></div><div id="localSTTCandidates"></div><div class="table-wrap"><table class="catalog-table"><thead><tr><th>МОДЕЛЬ / ПРОВАЙДЕР</th><th>ВОЗМОЖНОСТИ</th><th>ЛИМИТЫ / ЦЕНА</th><th>ДОБАВИТЬ</th></tr></thead><tbody id="catalogRows"></tbody></table></div><div class="catalog-paging"><span id="catalogTotal"></span><div class="tool-group"><button class="button small" id="catPrev">←</button><button class="button small" id="catNext">→</button></div></div><p class="note">FREE — нулевая цена в каталоге. «Настроен» — есть подключение, не подтверждение баланса или доступа. Транспорт выбирается явно.</p>`,visionOnly?'01 / ИЗВЛЕЧЕНИЕ':'МОДЕЛИ');
 let offset=0,serial=0,timer,group='all';const rowsElement=$('#catalogRows');
 let whyCache=null;
 const sourceLabel={config:'конфиг',catalog:'каталог',adapter:'адаптер',host:'хост',observed:'наблюдение'};
 const blockerLine=b=>`${esc((b.text||'').slice(0,160))} [${esc(sourceLabel[b.source]||b.source||'?')}]`;
 async function whyNotReady(){
  const panel=$('#catWhyPanel'),button=$('#catWhy');if(!panel)return;
  if(!panel.hidden){panel.hidden=true;button.setAttribute('aria-expanded','false');return;}
  panel.hidden=false;button.setAttribute('aria-expanded','true');
  try{
   if(!whyCache)whyCache=await api('/api/readiness');
   const r=whyCache,c=r.catalog||{},connections=(r.connections||[]).filter(x=>x.verdict&&!x.verdict.ready);
   const models=r.models||[],blocked=models.filter(x=>x.verdict&&!x.verdict.ready),usable=models.length-blocked.length;
   const seen=[],fresh=(r.observations||[]).filter(o=>{const k=o.connection_id+'/'+o.ref+'/'+o.outcome;if(seen.includes(k))return false;seen.push(k);return true;}).slice(0,5);
   panel.innerHTML=`<section class="vision-section"><p class="eyebrow">ПОЧЕМУ НЕ ГОТОВО</p><p class="note">Каталог: ${c.models||0} моделей · список published: <strong>${esc(c.offered_snapshot||'нет')}</strong>${c.runtime_version?` · host ${esc(c.runtime_version)}`:''}${c.last_error?` · ошибка обновления: ${esc(c.last_error)}`:''}</p>${connections.length?`<p class="note"><strong>Подключения:</strong></p>${connections.map(x=>`<div class="route-choice"><div><strong>${esc(x.adapter?.label||x.adapter?.provider_id||'?')}</strong><small>${(x.verdict.blockers||[]).map(blockerLine).join(' · ')||'без причины'}</small></div></div>`).join('')}`:''}<p class="note"><strong>Модели в пулах:</strong> ${usable} из ${models.length} готовы.</p>${blocked.length?blocked.map(x=>`<div class="route-choice"><div><strong>${esc(x.ref)}</strong><small>${esc((x.used_by||[]).join(', '))} · ${esc(x.reason||'без причины')}</small></div></div>`).join(''):'<p class="note">Все модели всех пулов имеют рабочую дорогу.</p>'}${fresh.length?`<p class="note"><strong>Недавние ответы хоста:</strong></p>${fresh.map(o=>`<p class="note">${esc(o.ref)} · ${esc(o.outcome)} · ${esc((o.text||'').slice(0,140))}</p>`).join('')}`:''}</section>`;
  }catch(error){panel.innerHTML=`<p class="note">Отчёт недоступен: ${esc(String(error&&error.message||error))}</p>`;}
 }
 async function load(){
  const serialNow=++serial,query={search:$('#catSearch').value,provider:$('#catProvider').value,limit:40,offset,vision:$('#catVision').checked,audio:$('#catAudio').checked,free:$('#catFree').checked,reasoning:$('#catReasoning').checked,ready:$('#catReady').checked};
  if(context.provider&&query.provider===context.provider)query.provider_id=context.provider;
  const params=new URLSearchParams({q:query.search,provider:query.provider,limit:40,offset});if(query.provider_id)params.set('provider_id',query.provider_id);for(const key of ['vision','audio','free','reasoning','ready'])if(query[key])params.set(key,'1');
  const page=group==='all'?await api('/api/catalog?'+params):await api('/api/catalog/selection','POST',{...query,references:group==='favorites'?state.config.control_center.favorite_models:[...new Set([...(workspaceInfo?.recent_models||[]),...state.config.control_center.recent_models])].slice(0,30),references_only:true});
  const capabilities=query.audio?await providerCapabilities():[];
  if(serialNow!==serial||!rowsElement.isConnected)return;
  renderLocalSTTCandidates(pool,query,capabilities);
  (page.items||[]).forEach(m=>state.meta.set(m.provider_id+'/'+m.model_id,m));
  $('#catalogRows').innerHTML=(page.items||[]).map(m=>{
   const ref=m.provider_id+'/'+m.model_id,vision=m.input_modalities?.includes('image'),audio=m.input_modalities?.includes('audio'),direct=!!nativeConnection(m),cost=m.free?'FREE':m.input_cost>0||m.output_cost>0?`$${m.input_cost} / $${m.output_cost} за 1M`:'Цена не указана';
   return `<tr><td><div class="catalog-name"><button class="favorite-model ${favoriteModel(ref)?'active':''}" data-favorite="${esc(ref)}" aria-label="${favoriteModel(ref)?'Убрать из избранного':'В избранное'}" aria-pressed="${favoriteModel(ref)}">${favoriteModel(ref)?'★':'☆'}</button><strong>${esc(m.model_name||m.model_id)}</strong></div><span class="ref">${esc(ref)}</span><small>${esc((m.description||'').slice(0,140))}</small></td><td><div class="tags">${m.free?tag('FREE','free'):''}${vision?tag('IMAGE','image'):''}${audio?tag('AUDIO','image'):''}${!vision&&!audio?tag('TEXT ONLY','text'):''}${m.reasoning?tag('REASONING'):''}${m.streaming?tag('STREAMING'):''}</div><small>${esc(m.status||'Каталог')}</small></td><td><span class="mono">${m.context?Math.round(m.context/1000)+'k':'—'} / ${m.output_limit||'—'}</span><small>${esc(cost)}</small></td><td><div class="tool-group">${direct&&context.transport!=='opencode'&&(!audio||connectionSupportsAudio(nativeConnection(m)))?`<button class="button small" data-select-model="${esc(ref)}" data-route="direct">Direct</button>`:''}${context.transport!=='direct'&&!audio?`<button class="button small" data-select-model="${esc(ref)}" data-route="opencode">Via OpenCode</button>`:''}</div></td></tr>`;
  }).join('')||`<tr><td colspan="4">${group==='favorites'?'Нет избранных моделей по этому фильтру. Добавьте модель звёздочкой.':group==='recent'?'Здесь появятся недавно вызванные и выбранные модели.':'Моделей по этим фильтрам нет.'}</td></tr>`;
  $('#catalogTotal').textContent=`${Math.min(offset+1,page.total)}–${Math.min(offset+(page.items||[]).length,page.total)} из ${page.total}`;$('#catPrev').disabled=offset===0;$('#catNext').disabled=offset+page.limit>=page.total;
  $$('[data-select-model]').forEach(b=>b.onclick=guarded(()=>selectCatalogModel(pool,state.meta.get(b.dataset.selectModel),b.dataset.route,visionOnly,context.connectionID)));
  $$('[data-select-local-stt]').forEach(b=>b.onclick=()=>selectLocalSTTModel(pool,b.dataset.selectLocalStt));
  $$('[data-favorite]').forEach(b=>b.onclick=()=>{toggleFavorite(b.dataset.favorite);refresh();});
 }
 const refresh=guarded(load);
 $('#catSearch').oninput=$('#catProvider').oninput=()=>{offset=0;clearTimeout(timer);timer=setTimeout(()=>{if($('#catalogRows'))refresh();},230);};
 for(const id of ['catVision','catAudio','catFree','catReasoning','catReady'])$('#'+id).onchange=()=>{offset=0;refresh();};
 $$('[data-catalog-group]').forEach(b=>b.onclick=()=>{group=b.dataset.catalogGroup;offset=0;$$('[data-catalog-group]').forEach(x=>x.classList.toggle('active',x===b));refresh();});
 $('#catPrev').onclick=()=>{offset=Math.max(0,offset-40);refresh();};$('#catNext').onclick=()=>{offset+=40;refresh();};
 $('#catWhy').onclick=guarded(whyNotReady);
$('#catRefresh').onclick=guarded(async()=>{const b=$('#catRefresh');b.disabled=true;try{await api('/api/catalog/refresh','POST');providerDirectoryCache=null;await load();}finally{if(b.isConnected)b.disabled=false;}});
 $('#catProvider').value=context.provider||'';refresh();$('#catSearch').focus();
}
