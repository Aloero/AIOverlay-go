'use strict';

// The model-settings surface, replacing the old connection page and the three source modes. The
// whole idea of the redesign is delegation: an application (a harness such as OpenCode or Hermes,
// or the direct protocol adapters) registers a provider, publishes its models and owns that
// model's parameters. This module renders what those applications report through /api/universe and
// never re-implements a harness' parameter logic here.

const modelSpace={snapshot:null,catalog:null,catalogPromise:null,freeOnly:false,allModels:false,query:'',providerFilter:'',appFilter:''};

function modelAppLabel(id){return {direct:'Direct',opencode:'OpenCode',hermes:'Hermes'}[id]||id;}

function matchesQuery(model,query){
	if(!query)return true;
	const hay=(model.ref+' '+(model.model_id||'')+' '+model.provider_id+' '+(model.input_modalities||[]).join(' ')).toLowerCase();
	return query.split(/\s+/).filter(Boolean).every(token=>hay.includes(token));
}

function modalityTags(model){
	const inp=model.input_modalities||[];
	const vision=inp.includes('image'),audio=inp.includes('audio');
	return (model.free?tag('FREE','free'):'')+(vision?tag('ВХОД: IMAGE','image'):'')+(audio?tag('ВХОД: AUDIO','image'):'')+(!vision&&!audio?tag('ВХОД: TEXT','text'):'')+tag('ВЫХОД: TEXT','text')+(model.reasoning?tag('REASONING'):'');
}

function renderModels(){
	modelSpace.snapshot=modelSpace.snapshot||null;
	$('#page').innerHTML=`<div class="page-head"><h1>Настройка моделей</h1><span class="dim" id="universeCount"></span></div>
	<div class="models-apps" id="modelsApps"></div>
	<div class="directory-toolbar"><input id="modelQuery" type="search" placeholder="Умный поиск: модель, семейство, provider/model…" aria-label="Поиск модели" value="${esc(modelSpace.query)}">
	<label class="switch"><input type="checkbox" id="modelFree" ${modelSpace.freeOnly?'checked':''}>Только FREE</label>
	<label class="switch"><input type="checkbox" id="modelAll" ${modelSpace.allModels?'checked':''}>Показать все модели</label>
	<button class="button small" id="modelAllReset" hidden>Только доступные</button></div>
	<div class="models-layout"><div class="models-providers"><h3 class="directory-section">Провайдеры</h3><div id="modelsProviders"></div></div>
	<div class="models-list"><h3 class="directory-section">Модели</h3><div id="modelsRows"></div></div></div>
	<p class="note">По умолчанию показаны модели тех провайдеров, которые вы можете использовать (есть ключ через приложение), и все бесплатные модели. «Показать все модели» добавляет остальные из каталога — они требуют регистрации. Параметры модели задаёт приложение, через которое она вызывается.</p>`;
	$('#modelQuery').oninput=e=>{modelSpace.query=e.target.value;rows();};
	$('#modelFree').onchange=e=>{modelSpace.freeOnly=e.target.checked;rows();};
	$('#modelAll').onchange=e=>{modelSpace.allModels=e.target.checked;$('#modelAllReset').hidden=!e.target.checked;rows();};
	$('#modelAllReset').onclick=()=>{modelSpace.allModels=false;$('#modelAll').checked=false;$('#modelAllReset').hidden=true;rows();};
	refresh(false);
}

function refresh(force){
	const finish=snapshot=>{modelSpace.snapshot=snapshot;renderApps();rows();};
	if(force||!modelSpace.snapshot){modelSpace.snapshot=null;modelSpace.catalog=null;guarded(async()=>{finish(await api('/api/universe'));})();return;}
	renderApps();rows();
}

function renderApps(){
	const root=$('#modelsApps');if(!root||!modelSpace.snapshot)return;
	root.innerHTML=`<p class="eyebrow">ПРИЛОЖЕНИЯ И ХАРНЕСЫ</p>`+modelSpace.snapshot.apps.map(app=>`<button class="app-chip ${app.available?'live':''} ${app.id===modelSpace.appFilter?'selected':''}" data-app="${esc(app.id)}" ${app.available?'':'disabled'}><strong>${esc(app.label)}</strong><small>${app.available?('доступно'+(app.version?' · '+app.version:'')):'не найдено'}</small></button>`).join('');
	$$('[data-app]').forEach(b=>b.onclick=()=>{modelSpace.appFilter=modelSpace.appFilter===b.dataset.app?'':b.dataset.app;renderApps();rows();});
}

// loadCatalogAll extends the default surface with the rest of the catalog, through the same
// catalog endpoint the pipeline picker uses — universe entries win because they carry the app
// mapping. Bounded: the catalog is finite and the loop stops at the page total.
function loadCatalogAll(){
	if(modelSpace.catalog)return Promise.resolve(modelSpace.catalog);
	if(modelSpace.catalogPromise)return modelSpace.catalogPromise;
	modelSpace.catalogPromise=(async()=>{
		const merged=new Map((modelSpace.snapshot?.models||[]).map(m=>[m.ref,{...m}]));
		let offset=0,total=Infinity;
		while(offset<total&&offset<4000){
			const page=await api('/api/catalog?limit=500&offset='+offset);
			const items=page.items||[];
			total=typeof page.total==='number'?page.total:offset+items.length;
			for(const item of items){
				const ref=item.provider_id+'/'+item.model_id;
				if(merged.has(ref))continue;
				merged.set(ref,{ref,provider_id:item.provider_id,provider_name:item.provider_name||item.provider_id,model_id:item.model_id,
					input_modalities:item.input_modalities,free:item.free,reasoning:item.reasoning,context:item.context,
					ready:item.ready,reason:item.ready?'':(item.status||'провайдер не зарегистрирован'),apps:[]});
			}
			offset+=500;
			if(!items.length)break;
		}
		modelSpace.catalog=[...merged.values()];
		return modelSpace.catalog;
	})().finally(()=>{modelSpace.catalogPromise=null;});
	return modelSpace.catalogPromise;
}

let rowsSerial=0;
async function rows(){
	const root=$('#modelsRows'),providersRoot=$('#modelsProviders');
	if(!root||!modelSpace.snapshot)return;
	const serial=++rowsSerial;
	const snapshot=modelSpace.snapshot;
	let source=snapshot.models||[];
	if(modelSpace.allModels){
		source=await loadCatalogAll();
		if(serial!==rowsSerial||!root.isConnected)return;
	}
	const providerName=id=>{const p=snapshot.providers.find(x=>x.id===id);return p?(p.name||p.id):(source.find(m=>m.provider_id===id)?.provider_name||id);};
	let models=source;
	if(!modelSpace.allModels){
		// The default surface: usable models only - a route is ready, or the model is free.
		models=models.filter(m=>m.ready||m.free);
	}
	if(modelSpace.freeOnly)models=models.filter(m=>m.free);
	if(modelSpace.appFilter)models=models.filter(m=>(m.apps||[]).includes(modelSpace.appFilter));
	if(modelSpace.providerFilter)models=models.filter(m=>m.provider_id===modelSpace.providerFilter);
	models=models.filter(m=>matchesQuery(m,modelSpace.query.trim().toLowerCase()));
	$('#universeCount').textContent=(modelSpace.allModels?source.length+' · показан весь каталог':(snapshot.models||[]).length+' моделей в каталоге')+' · '+(snapshot.snapshot?'снимок '+snapshot.snapshot:'нет снимка');

	// Rail: providers the universe knows, plus catalog-only providers visible in all-models mode.
	const rail=snapshot.providers.slice();
	if(modelSpace.allModels){
		const known=new Set(rail.map(p=>p.id));
		const extra=new Map();
		for(const m of source){
			if(known.has(m.provider_id))continue;
			const row=extra.get(m.provider_id)||{id:m.provider_id,name:m.provider_name||m.provider_id,models:0,apps:[]};
			row.models++;extra.set(m.provider_id,row);
		}
		rail.push(...extra.values());
	}
	providersRoot.innerHTML=rail.map(p=>{
		const apps=(p.apps||[]).map(app=>`<span class="provider-app ${app===p.registered_via?'active':''}" data-provider-app="${esc(p.id)}" data-app="${esc(app)}">${esc(modelAppLabel(app))}</span>`).join('');
		const status=p.registered_via?('через '+esc(modelAppLabel(p.registered_via))+(p.keyed?' · ключ есть':' · без ключа')):'не зарегистрирован';
		return `<div class="saved-connection ${p.id===modelSpace.providerFilter?'selected':''}" data-provider-row="${esc(p.id)}"><div><strong>${esc(p.name||p.id)}</strong><small>${esc(status)} · ${p.models||0} моделей</small></div><div class="tags">${apps}</div></div>`;
	}).join('')||'<p class="note">Нет провайдеров в снимке.</p>';
	$$('[data-provider-row]').forEach(el=>el.onclick=()=>{modelSpace.providerFilter=modelSpace.providerFilter===el.dataset.providerRow?'':el.dataset.providerRow;rows();});
	$$('[data-provider-app]').forEach(el=>el.onclick=event=>{event.stopPropagation();openProviderApps(el.dataset.providerApp,el.dataset.app);});

	const targetPool=()=>selectedNode()?.model_pool||pipe()?.nodes.find(n=>n.model_pool)?.model_pool;
	root.innerHTML=models.map(m=>{
		const status=m.ready?tag('ГОТОВА','ready'):tag('НЕ ГОТОВА','warn');
		const apps=(m.apps||[]).map(app=>modelAppLabel(app)).join(' · ')||'—';
		return `<div class="model-card" data-model="${esc(m.ref)}"><div class="model-card-head"><strong>${esc(m.model_id)}</strong><small>${esc(providerName(m.provider_id))} · ${esc(apps)}</small></div>
		<div class="tags">${modalityTags(m)}${status}</div>
		${m.ready?'':`<p class="note">${esc(m.reason||'не готова')}</p>`}
		<div class="tool-group"><button class="button small" data-add-model="${esc(m.ref)}">В пайплайн</button><button class="button small" data-model-params="${esc(m.ref)}" ${m.ready?'':'disabled'} title="Параметры задаёт приложение">Параметры…</button></div></div>`;
	}).join('')||'<p class="note">Нет моделей под этот фильтр.</p>';

	$$('[data-add-model]').forEach(b=>b.onclick=guarded(()=>{
		const model=findUniverseModel(b.dataset.addModel),pool=targetPool();
		if(!pool)throw new Error('Откройте этап моделей, чтобы добавить в него модель.');
		addUniverseModelToPool(pool,model);
	}));
	$$('[data-model-params]').forEach(b=>b.onclick=()=>openModelParams(findUniverseModel(b.dataset.modelParams)));
}

function findUniverseModel(ref){
	const list=(modelSpace.allModels&&modelSpace.catalog)||modelSpace.snapshot?.models||[];
	return list.find(m=>m.ref===ref);
}

function addUniverseModelToPool(pool,model){
	if(!pool||!model)throw new Error('Модель не найдена.');
	const app=(model.apps||[])[0]||'direct';
	let connection=state.config.providers.find(p=>p.enabled&&p.kind!=='local_stt'&&p.kind!=='openai_live'&&p.opencode_provider_id===model.provider_id&&p.kind==='embedded_opencode');
	if(app!=='opencode')connection=state.config.providers.find(p=>p.enabled&&p.catalog_provider_id===model.provider_id&&p.kind!=='embedded_opencode'&&p.kind!=='local_stt'&&p.kind!=='openai_live');
	if(!connection)connection=state.config.providers.find(p=>p.enabled&&p.kind!=='local_stt'&&p.kind!=='openai_live'&&catalogProviderID(p)===model.provider_id);
	if(!connection){
		// An all-catalog model whose provider is not registered yet: the action is registration,
		// not a half-added slot that cannot answer.
		const native=state.config.providers.find(p=>catalogProviderID(p)===model.provider_id&&!['embedded_opencode','embedded_hermes','local_stt','openai_live'].includes(p.kind));
		if(native){notify('Подключение «'+native.label+'» сейчас не готово: '+(model.reason||'нет маршрута'));return;}
		registerProviderApp({id:model.provider_id,name:model.provider_name||model.provider_id,credential_env:[]},'direct');
		return;
	}
	if(!connection.enabled)throw new Error('Подключение к провайдеру выключено.');
	const id=connection.kind==='embedded_opencode'?model.provider_id+'/'+model.model_id:model.model_id;
	pool.models=pool.models||[];
	if(pool.models.some(s=>s.provider_id===connection.id&&s.model_id===id)){notify('Модель уже в списке.');return;}
	pool.models.push({id:uid('model'),provider_id:connection.id,model_id:id,enabled:true,priority:100-pool.models.length,weight:1,quality_score:80,timeout_ms:0,first_token_timeout_ms:0,max_attempts:1});
	dirty();notify('Модель добавлена в '+pool.result_name);
}

// openProviderApps: choose the application that fronts a provider, then register through it. The
// list is exactly the apps the universe says can carry the provider and that are available now.
function openProviderApps(providerID,wantApp){
	const snapshot=modelSpace.snapshot;if(!snapshot)return;
	const provider=snapshot.providers.find(p=>p.id===providerID);if(!provider)return;
	const available=new Set(snapshot.apps.filter(a=>a.available).map(a=>a.id));
	const apps=(provider.apps||[]).filter(app=>available.has(app));
	if(!apps.length){notify('Ни одно приложение сейчас не может нести этого провайдера.');return;}
	guarded(async()=>{
		const directory=await providerDirectory();
		const meta={id:providerID,name:provider.name||providerID,credential_env:(directory.find(d=>d.id===providerID)||{}).credential_env||[]};
		if(apps.length===1){registerProviderApp(meta,apps[0]);return;}
		modal('Приложение для '+(provider.name||providerID),`<p class="note">Список моделей и параметры задаёт выбранное приложение.</p>`+apps.map(app=>`<button class="route-choice" data-register-app="${esc(app)}"><strong>${esc(modelAppLabel(app))}</strong><small>${app==='opencode'?'встроенный runtime':app==='hermes'?'внешний харнес (нужен PATH)':'протокол приложения'}</small></button>`).join(''),'РЕГИСТРАЦИЯ');
		$$('[data-register-app]').forEach(b=>b.onclick=guarded(async()=>{await closeModal(true);registerProviderApp(meta,b.dataset.registerApp);}));
	})();
}

function registerProviderApp(meta,app){
	let connection;
	if(app==='opencode'){connection=newOpenCodeConnection(meta);}
	else if(app==='hermes'){connection=providerTemplate('hm_'+meta.id.replace(/[^a-z\d_-]/gi,'_'),meta.name||meta.id);connection.kind='embedded_hermes';connection.catalog_provider_id=meta.id;connection.credential_env=meta.credential_env||[];}
	else{connection=providerTemplate(meta.id.replace(/[^a-z\d_-]/gi,'_'),meta.name||meta.id);connection.catalog_provider_id=meta.id;connection.credential_env=meta.credential_env||[];}
	if(provider(connection.id))connection.id=uid(connection.id);
	providerDialog(connection);
}

// App-owned model parameters. Only reasoning effort is offered from the catalog today; the value
// is stored per slot so the application that carries the model (opencode, hermes) interprets it.
// The overlay deliberately does not replicate any harness' parameter logic.
function slotDeliveryApp(slot){
	const p=provider(slot?.provider_id);
	if(!p)return 'direct';
	if(p.kind==='embedded_opencode')return 'opencode';
	if(p.kind==='embedded_hermes')return 'hermes';
	return 'direct';
}

function openSlotAppParams(slot){
	if(!slot)return;
	const app=slotDeliveryApp(slot);
	// app_params values are complete JSON values (RawMessage on the Go side), so a stored
	// scalar may arrive either bare or quoted after an older round trip; both read the same.
	const raw=slot.app_params?.reasoning;
	const current=typeof raw==='string'?raw.replace(/^"|"$/g,''):'';
	const options={'':'Как задано приложением','none':'Выключить рассуждение','low':'Низкое','medium':'Среднее','high':'Высокое'};
	const note=app==='direct'
		?'Прямые протоколы не интерпретируют app_params — значение применится, только если модель вызывается через приложение.'
		:'Приложение «'+esc(modelAppLabel(app))+'» само трактует это значение и умеет больше, чем показывает Studio: параметры не дублируются здесь намеренно.';
	modal('Параметры · '+slot.model_id,`${selectField('Рассуждение','mpReasoning',options,current)}<p class="note">${note}</p><div class="dialog-footer"><button class="button primary" id="mpSave">Сохранить</button></div>`,'ПАРАМЕТРЫ МОДЕЛИ');
	$('#mpSave').onclick=guarded(async()=>{
		const value=$('#mpReasoning')?$('#mpReasoning').value:'';
		slot.app_params=slot.app_params||{};
		if(value)slot.app_params.reasoning=value;else delete slot.app_params.reasoning;
		dirty();await saveConfig();await closeModal(true);notify('Параметр сохранён для приложения.');renderPage();
	});
}

function openModelParams(model){
	if(!model)return;
	const pool=selectedNode()?.model_pool||pipe()?.nodes.find(n=>n.model_pool)?.model_pool;
	const slot=pool?.models?.find(s=>(s.provider_id+'/'+s.model_id===model.ref||s.model_id===model.model_id)&&s.provider_id&&provider(s.provider_id)&&catalogProviderID(provider(s.provider_id))===model.provider_id);
	if(!slot){notify('Откройте этап с этой моделью, чтобы задать её параметры.');return;}
	openSlotAppParams(slot);
}
