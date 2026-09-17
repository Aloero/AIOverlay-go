'use strict';

const sourceNames={manual:'Мой список',catalog:'Все доступные модели',manual_then_catalog:'Мой список → весь каталог'};
const skipNames={not_configured:'нет подключения / ключа',not_free:'не FREE',needs_vision:'не принимает изображение',text_only_filter:'не TEXT ONLY',quality_floor:'ниже порога качества',disabled:'отключены',non_text_output:'нет текстового ответа'};

function renderPoolInspector(n) {
 const pool=n.model_pool;
 $('#inspectorBody').innerHTML=`${field('Название этапа','nodeName',n.name)}
 <label class="switch stage-switch"><input type="checkbox" id="enableVision" ${pool.vision_preprocessor?'checked':''}>Зрение → текстовая модель</label><div id="visionPool"></div><div id="solverSource"></div><div class="section-line"><h4>ПОРЯДОК ЗАПУСКА</h4></div>
 ${selectField('Режим','poolPolicy',policyNames,pool.policy)}<div id="dispatchControls"></div><div id="poolPlan" class="pool-plan"></div>
 <div class="section-line"><h4>${pool.selection?.mode==='catalog'?'ПРИОРИТЕТНЫЕ МОДЕЛИ':'МОЙ СПИСОК'}</h4></div><div id="solverModels"></div><button class="add-model" id="addSolverModel">+ Выбрать модель</button>
 ${outgoing(n.id)}`;
 bindValue('nodeName',n,'name',false,renderGraph);
 renderSourceControls($('#solverSource'),pool,'source',false);
 renderSlots($('#solverModels'),pool,false);
 $('#addSolverModel').onclick=()=>openCatalog(pool,false);
 $('#poolPolicy').onchange=e=>{pool.policy=e.target.value;dirty();renderGraph();renderDispatchControls(pool);refreshPoolPlan(pool);};
 $('#enableVision').onchange=e=>{if(e.target.checked)pool.vision_preprocessor=defaultVisionPool();else delete pool.vision_preprocessor;dirty();renderGraph();renderInspector();};
 if(pool.vision_preprocessor) {
  const vision=pool.vision_preprocessor;
  $('#visionPool').innerHTML=`<div class="vision-section"><p class="eyebrow">01 / ЗРЕНИЕ</p><div id="visionSource"></div><div id="visionModels"></div><button class="add-model" id="addVisionModel">+ Модель со зрением</button>${selectField('Запуск VLM','visionPolicy',policyNames,vision.policy)}<div class="form-row">${field('Одновременно','visionParallel',vision.max_parallel,'number')}${field('Вызовов','visionCalls',vision.max_calls,'number')}</div>${field('Задержка резерва, мс','visionHedge',vision.hedge_delay_ms,'number')}<button class="button small" id="visionPlan">Очередь VLM</button><div class="stage-output">↓ {{vision.text}}</div><p class="eyebrow">02 / РЕШЕНИЕ</p></div>`;
  renderSourceControls($('#visionSource'),vision,'visionSource',true);renderSlots($('#visionModels'),vision,true);
  $('#addVisionModel').onclick=()=>openCatalog(vision,true);
  for(const [id,key,numeric]of [['visionPolicy','policy',false],['visionParallel','max_parallel',true],['visionCalls','max_calls',true],['visionHedge','hedge_delay_ms',true]])bindValue(id,vision,key,numeric,renderGraph);
  $('#visionPlan').onclick=guarded(()=>showPoolPlan(vision,true));
 }
 renderDispatchControls(pool);refreshPoolPlan(pool);bindOutgoing();
}

function renderSourceControls(root,pool,prefix,isVision) {
 const mode=pool.selection?.mode||'manual',auto=mode!=='manual';
 root.innerHTML=selectField(isVision?'Модели VLM':'Источник моделей',prefix+'Mode',sourceNames,mode)+
 (auto?`<div class="source-settings"><label class="switch"><input type="checkbox" id="${prefix}Free" ${pool.selection.free_only?'checked':''}>Только FREE</label>${selectField('Вход модели',prefix+'Modality',isVision?{image:'IMAGE'}:{auto:'По входу этапа',image:'Только IMAGE',text:'Только TEXT ONLY'},isVision?'image':pool.selection.modality||'auto')}<details><summary>Подключения и качество</summary><div class="provider-scope" id="${prefix}Providers"></div><div class="form-row">${field('Порог качества',prefix+'Floor',pool.quality_floor,'number')}${field('Оценка остальных',prefix+'Quality',pool.selection.default_quality_score||0,'number','В Models.dev нет оценки качества. 0 означает отсутствие вашей оценки. Порог выше 0 исключает такие модели.')}</div></details></div>`:'');
 $('#'+prefix+'Mode').onchange=e=>{
  const next=e.target.value;
  pool.selection||={mode:'manual',provider_ids:[],free_only:true,modality:isVision?'image':'auto',default_quality_score:0};
  pool.selection.mode=next;dirty();renderGraph();renderInspector();
 };
 if(!auto)return;
 $('#'+prefix+'Free').onchange=e=>{pool.selection.free_only=e.target.checked;dirty();renderGraph();if(isVision)renderInspector();else refreshPoolPlan(pool);};
 $('#'+prefix+'Modality').onchange=e=>{
  pool.selection.modality=e.target.value;
  if(!isVision && e.target.value==='text'&&!pool.vision_preprocessor)pool.vision_preprocessor=defaultVisionPool();
  dirty();renderGraph();renderInspector();
 };
 const scope=$('#'+prefix+'Providers'),selected=pool.selection.provider_ids||[];
 scope.innerHTML=`<label class="switch"><input type="checkbox" data-all-routes ${selected.length===0?'checked':''}>Все включённые подключения</label>`+state.config.providers.map(p=>`<label class="route-option"><input type="checkbox" data-scope-route="${esc(p.id)}" ${selected.length===0||selected.includes(p.id)?'checked':''}><span>${esc(p.label)}<small>${esc(routeLabel(p))}${!p.enabled?' · выключено':''}</small></span></label>`).join('');
 $('[data-all-routes]',scope).onchange=e=>{pool.selection.provider_ids=e.target.checked?[]:state.config.providers.map(p=>p.id);dirty();renderInspector();};
 $$('[data-scope-route]',scope).forEach(el=>el.onchange=()=>{
  const ids=$$('[data-scope-route]:checked',scope).map(e=>e.dataset.scopeRoute);
  if(!ids.length){el.checked=true;notify('Оставьте хотя бы одно подключение.');return;}
  pool.selection.provider_ids=ids;dirty();if(isVision)renderInspector();else refreshPoolPlan(pool);
 });
 for(const [id,obj,key]of [[prefix+'Floor',pool,'quality_floor'],[prefix+'Quality',pool.selection,'default_quality_score']])bindValue(id,obj,key,true,()=>{renderGraph();if(!isVision)refreshPoolPlan(pool);});
}

function renderDispatchControls(pool) {
 const racing=['parallel_race','staggered_race'].includes(pool.policy);
 $('#dispatchControls').innerHTML=`<div class="form-row">${field('Вызовов на этап','dispatchCalls',pool.max_calls,'number')}${racing?field('Одновременно','dispatchParallel',pool.max_parallel,'number'):''}</div>${pool.policy==='staggered_race'?field('Запуск резерва через, мс','dispatchHedge',pool.hedge_delay_ms,'number'):''}<div class="dispatch-effective" data-help="Действует меньшее из ограничений этапа и всего пайплайна. Новый ключ и повтор на том же ключе расходуют отдельный вызов. Первый завершённый непустой ответ побеждает; другие попытки отменяются.">Фактически: до ${racing?Math.min(pool.max_parallel,pipe().budget.max_parallel_calls):1} одновременно · до ${Math.min(pool.max_calls,pipe().budget.max_model_calls)} вызовов</div>`;
 for(const [id,key]of [['dispatchCalls','max_calls'],['dispatchParallel','max_parallel'],['dispatchHedge','hedge_delay_ms']])bindValue(id,pool,key,true,()=>{renderGraph();renderDispatchControls(pool);refreshPoolPlan(pool);});
}

let planSerial=0;
async function requestPoolPlan(pool,image) {return api('/api/pool/plan','POST',{config:state.config,pool,image});}
async function refreshPoolPlan(pool) {
 const root=$('#poolPlan');if(!root)return;
 const serial=++planSerial;root.textContent='Составление очереди…';
 try {
  const plan=await requestPoolPlan(pool,!pool.vision_preprocessor);
  if(serial!==planSerial||!root.isConnected)return;
  const candidates=plan.candidates||[],limit=Math.min(pool.max_calls,pipe().budget.max_model_calls);
  root.innerHTML=`<div class="plan-heading"><strong>${candidates.length} моделей</strong><span>≤ ${limit} вызовов</span></div><div class="schedule-preview">${candidates.slice(0,4).map((m,i)=>`<div><time>${esc(dispatchWhen(pool,i))}</time><span>${esc(shortModel(m.model_id))}</span></div>`).join('')||'<p class="note">Подходящих моделей нет.</p>'}</div><button class="button small" id="showPoolPlan">Вся очередь / пропуски</button>${plan.skipped?.quality_floor>0&&pool.selection?.mode!=='manual'&&pool.selection?`<button class="text-button allow-unscored" id="allowUnscored">Разрешить модели без оценки (${plan.skipped.quality_floor})</button>`:''}`;
  $('#showPoolPlan').onclick=()=>showPoolPlan(pool,!pool.vision_preprocessor,plan);
  if($('#allowUnscored'))$('#allowUnscored').onclick=()=>{pool.quality_floor=0;dirty();renderGraph();renderInspector();};
 }catch(e){if(root.isConnected)root.textContent='Очередь недоступна: '+e.message;}
}
function dispatchWhen(pool,i) {
 if(i===0)return 'Старт';
 if(pool.policy==='parallel_race')return i<Math.min(pool.max_parallel,pipe().budget.max_parallel_calls)?'Старт':'Свободный слот';
 if(pool.policy==='staggered_race')return i<Math.min(pool.max_parallel,pipe().budget.max_parallel_calls)?'+'+(i*pool.hedge_delay_ms/1000)+' с':'Свободный слот';
 return 'После ошибки';
}
async function showPoolPlan(pool,image,existing) {
 const plan=existing||await requestPoolPlan(pool,image),candidates=plan.candidates||[];
 let offset=0;
 modal('Очередь попыток',`<div class="plan-totals"><span>${candidates.length} моделей</span><span>До ${Math.min(pool.max_calls,pipe().budget.max_model_calls)} вызовов</span><span>${esc(policyNames[pool.policy])}</span></div><div class="note plan-explanation">${pool.policy==='staggered_race'?'Первый запрос стартует сразу. Пока ответа нет, резерв запускается с указанным интервалом; после ошибки свободное место занимается раньше.':pool.policy==='parallel_race'?'Заполняются свободные места в пределах параллельного лимита. Первый успешный ответ завершает этап.':'После ошибки выбирается следующая модель. При успехе перебор останавливается.'} Повторные ключи тоже расходуют вызовы. Показан приоритетный список; чередование, веса и статистика могут изменить стартовую модель.</div><div class="table-wrap"><table class="queue-table"><thead><tr><th>№</th><th>Модель</th><th>Подключение</th><th>Приоритет</th><th>Качество</th></tr></thead><tbody id="queueRows"></tbody></table></div><div class="catalog-paging"><span id="queueCount"></span><div><button class="button small" id="queuePrev">←</button><button class="button small" id="queueNext">→</button></div></div><details class="plan-skips"><summary>Отбор моделей</summary>${Object.entries(plan.skipped||{}).map(([reason,count])=>`<div>${esc(skipNames[reason]||reason)} <b>${count}</b></div>`).join('')||'<p>Ничего не пропущено.</p>'}</details>`,'ПЛАН · БЕЗ ЗАПРОСОВ К МОДЕЛЯМ');
 function page() {
  $('#queueRows').innerHTML=candidates.slice(offset,offset+60).map((m,i)=>`<tr><td>${offset+i+1}</td><td class="mono">${esc(m.model_id)}</td><td>${esc(provider(m.provider_id)?.label||m.provider_id)}<small>${esc(routeLabel(provider(m.provider_id)))}</small></td><td>${m.priority}</td><td>${m.quality_score||'Не задано'}</td></tr>`).join('');
  $('#queueCount').textContent=`${Math.min(offset+1,candidates.length)}–${Math.min(offset+60,candidates.length)} / ${candidates.length}`;
  $('#queuePrev').disabled=offset===0;$('#queueNext').disabled=offset+60>=candidates.length;
 }
 $('#queuePrev').onclick=()=>{offset=Math.max(0,offset-60);page();};$('#queueNext').onclick=()=>{offset+=60;page();};page();
}
