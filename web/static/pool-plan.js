'use strict';

let planSerial=0;
async function requestPoolPlan(pool,image,nodeID=state.node){return api('/api/pool/plan','POST',{config:state.config,pool,image,pipeline_id:pipe().id,node_id:nodeID});}
function planFilters(pool){
 const s=pool.selection,scope=s?.provider_ids?.length?s.provider_ids.map(id=>provider(id)?.label||id).join(', '):'включённые подключения';
 return s&&s.mode!=='manual'?scope+' · '+(s.modality==='image'?'IMAGE':s.modality==='text'?'TEXT ONLY':'по входу этапа')+(s.free_only?' · только FREE':' · включая платные'):'только модели из списка';
}
function emptyQueueMarkup(plan,pool){
 const skip=plan.skipped||{},count=Object.values(skip).reduce((a,b)=>a+b,0);
 if(skip.quality_floor)return `<p class="queue-empty">${skip.quality_floor} моделей исключено порогом качества.</p><button class="text-button" data-plan-action="quality">Настроить качество</button>`;
 if(skip.not_configured)return '<p class="queue-empty">Для выбранных моделей нет включённого подключения с ключом.</p><button class="text-button" data-plan-action="providers">Открыть подключения</button>';
 if(skip.needs_vision||skip.text_only_filter)return '<p class="queue-empty">Модальности этапа и подключений не совпадают.</p><button class="text-button" data-plan-action="modality">Проверить вход этапа</button>';
 if(skip.not_free)return '<p class="queue-empty">Фильтр FREE исключил модели этих подключений.</p><button class="text-button" data-plan-action="filters">Открыть фильтры</button>';
 return `<p class="queue-empty">${count?'Все кандидаты исключены.':'Очередь пуста.'}</p><button class="text-button" data-plan-action="models">Выбрать модели</button>`;
}
function bindPlanActions(root,pool){
 $$('[data-plan-action]',root).forEach(el=>el.onclick=guarded(async()=>{
  const action=el.dataset.planAction;
  if(action==='providers'){await openSettingTarget({page:'providers',field:'providerSearch'});return;}
  if(action==='models'){if($('#dialog').open)await closeModal();openCatalog(pool);return;}
  const field={quality:'sourceFloor',modality:'sourceModality',filters:'sourceFree'}[action];
  await openSettingTarget({page:'pipelines',nodeID:state.node,tab:'models',field});
 }));
}
function plannedStartsMarkup(pool,candidates){
 const mode=poolDispatch(pool),cap=Math.min(pool.max_parallel,pipe().budget.max_parallel_calls),rows=candidates.slice(0,Math.min(4,pool.max_calls,pipe().budget.max_model_calls));
 if(mode==='sequential')return `<div class="schedule-preview">${rows.map((m,i)=>`<div><time>${i?'После ошибки':'Старт'}</time><span>${esc(shortModel(m.model_id))}</span></div>`).join('')}</div>`;
 const step=pool.hedge_delay_ms/1000,maxTime=Math.max(1,(Math.min(rows.length,cap)-1)*step);
 return `<div class="planned-timeline" aria-label="План запусков, не прогноз времени ответа"><div class="timeline-axis"><span>0 с</span><span>${mode==='staggered_race'?maxTime+' с':'свободные места'}</span></div>${rows.map((m,i)=>{const start=mode==='parallel_race'?0:i*step;return `<div class="planned-row"><span title="${esc(m.model_id)}">${esc(shortModel(m.model_id))}</span><div class="planned-track">${i<cap?`<i style="left:${mode==='staggered_race'?start/(maxTime+1)*80:0}%"></i><small style="left:${mode==='staggered_race'?start/(maxTime+1)*80:0}%">${start?'+'+start+' с':'Старт'}</small>`:'<small>Освободится место</small>'}</div></div>`;}).join('')}</div>`;
}
async function refreshPoolPlan(pool){
 const root=$('#poolPlan');if(!root)return;const serial=++planSerial;root.textContent='Составление очереди…';
 try{
  const plan=await requestPoolPlan(pool,!pool.vision_preprocessor);
  if(serial!==planSerial||!root.isConnected)return;
  const candidates=plan.candidates||[],limit=Math.min(pool.max_calls,pipe().budget.max_model_calls);
  root.innerHTML=`<div class="plan-heading"><strong>${candidates.length} моделей</strong><span>≤ ${limit} вызовов</span></div><div class="plan-filter-label">${esc(planFilters(pool))}</div>${candidates.length?plannedStartsMarkup(pool,candidates):emptyQueueMarkup(plan,pool)}<div class="plan-footnote">Повтор или другой ключ тоже расходует вызов.</div><button class="button small" id="showPoolPlan">Очередь и причины отбора</button>${plan.skipped?.quality_floor>0&&pool.selection?.mode!=='manual'&&pool.selection?`<button class="text-button allow-unscored" id="allowUnscored">Разрешить модели без оценки (${plan.skipped.quality_floor})</button>`:''}`;
  $('#showPoolPlan').onclick=guarded(()=>showPoolPlan(pool,!pool.vision_preprocessor,plan));
  if($('#allowUnscored'))$('#allowUnscored').onclick=()=>{pool.quality_floor=0;dirty();renderGraph();renderInspector();};
  bindPlanActions(root,pool);
 }catch(e){if(root.isConnected)root.textContent='Очередь недоступна: '+e.message;}
}
function dispatchWhen(pool,i){
 if(!i)return 'Старт';const mode=poolDispatch(pool),cap=Math.min(pool.max_parallel,pipe().budget.max_parallel_calls);
 if(mode==='parallel_race')return i<cap?'Старт':'Свободный слот';
 if(mode==='staggered_race')return i<cap?'+'+(i*pool.hedge_delay_ms/1000)+' с':'Свободный слот';return 'После ошибки';
}
async function showPoolPlan(pool,image,existing){
 const plan=existing||await requestPoolPlan(pool,image),candidates=plan.candidates||[];let offset=0;
 const mode=poolDispatch(pool),order=poolOrder(pool);
 modal('План следующего запроса',`<div class="plan-totals"><span>${candidates.length} моделей</span><span>До ${Math.min(pool.max_calls,pipe().budget.max_model_calls)} вызовов</span><span>${esc(orderNames[order])}</span><span>${esc(scheduleCaption(pool))}</span></div><p class="note">${esc(planFilters(pool))}</p>${candidates.length?plannedStartsMarkup(pool,candidates):emptyQueueMarkup(plan,pool)}<p class="note plan-explanation">${mode==='staggered_race'?'Резерв стартует по интервалу, пока нет принятого ответа. Ошибка освобождает место раньше.':mode==='parallel_race'?'Одновременно запускаются запросы в пределах общего лимита. Остальные ждут свободного места.':'После ошибки используется следующий ключ или модель. После успеха перебор заканчивается.'} Повторные ключи тоже расходуют вызовы. ${order==='weighted'?'Первая модель выбирается случайно по весам; этот порядок — пример.':'Показан снимок текущего порядка без расходования очереди.'} Это план запусков, не прогноз длительности ответов.</p><div class="table-wrap"><table class="queue-table"><thead><tr><th>№</th><th>Модель</th><th>Подключение</th><th>Приоритет</th><th>Качество</th></tr></thead><tbody id="queueRows"></tbody></table></div><div class="catalog-paging"><span id="queueCount"></span><div><button class="button small" id="queuePrev">←</button><button class="button small" id="queueNext">→</button></div></div><details class="plan-skips"><summary>Почему другие модели не участвуют</summary>${Object.entries(plan.skipped||{}).map(([reason,count])=>`<div>${esc(skipNames[reason]||reason)} <b>${count}</b></div>`).join('')||'<p>Ничего не пропущено.</p>'}</details>`,'БЕЗ ЗАХВАТА И AI-ЗАПРОСОВ');
 function page(){
  $('#queueRows').innerHTML=candidates.slice(offset,offset+60).map((m,i)=>`<tr><td>${offset+i+1}</td><td class="mono">${esc(m.model_id)}</td><td>${esc(provider(m.provider_id)?.label||m.provider_id)}<small>${esc(routeLabel(provider(m.provider_id)))}</small></td><td>${m.priority}</td><td>${m.quality_score||'Не задано'}</td></tr>`).join('');
  $('#queueCount').textContent=`${Math.min(offset+1,candidates.length)}–${Math.min(offset+60,candidates.length)} / ${candidates.length}`;
  $('#queuePrev').disabled=offset===0;$('#queueNext').disabled=offset+60>=candidates.length;
 }
 $('#queuePrev').onclick=()=>{offset=Math.max(0,offset-60);page();};$('#queueNext').onclick=()=>{offset+=60;page();};page();bindPlanActions($('#dialogBody'),pool);
}
