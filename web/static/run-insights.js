'use strict';

function attemptRows(trace){
 const rows=new Map();let anonymous=0;
 for(const e of trace?.events||[]){
  if(!e.attempt)continue;const a=e.attempt,key=a.id||[e.node_id,a.provider_id,a.model_id,a.credential_id].join('|');
  if(e.kind==='attempt_start')rows.set(key,{key:key||String(++anonymous),node:e.node_id,attempt:a,start:e.at_ms||0,end:null,first:null,outcome:null});
  const row=rows.get(key);if(!row)continue;
  if(e.kind==='first_token')row.first=e.at_ms;
  if(e.kind==='attempt_end'){row.end=e.at_ms;row.outcome=e.outcome;row.duration=e.duration_ms;row.ttft=e.ttft_ms||0;row.tps=e.tps||0;row.tokens=e.output_tokens||0;row.reason=e.message||'';if(row.first===null&&e.ttft_ms>0)row.first=row.start+e.ttft_ms;}
 }
 return [...rows.values()].sort((a,b)=>a.start-b.start);
}
function cancellationLabel(row,trace,job){
 const winner=(trace?.events||[]).find(e=>e.kind==='winner'&&e.node_id===row.node&&e.attempt?.id!==row.attempt?.id&&e.at_ms>=row.start&&e.at_ms<=(row.end??Infinity));
 return row.reason==='race_lost'||winner?'Остановлен после победы другой модели':'Отменён пользователем';
}
function runSummaryMarkup(trace,job={}){
 const rows=attemptRows(trace),events=trace?.events||[],winner=trace?.winner,winning=rows.find(r=>r.attempt.id&&r.attempt.id===winner?.id)||rows.findLast(r=>r.outcome==='success'),calls=rows.length,max=trace?.budget?.max_model_calls,duration=finite(job.duration_ms,(trace?.finished_at&&trace?.started_at)?Date.parse(trace.finished_at)-Date.parse(trace.started_at):0),budget=events.findLast(e=>e.kind==='budget_exhausted');
 const repeats=Math.max(0,calls-new Set(rows.map(r=>[r.node,r.attempt.provider_id,r.attempt.model_id].join('/'))).size);
 let heading='Запрос ещё не запускался.',detail='';
 if(job.running){heading=job.phase==='preparing'?'Готовлю входные данные…':`Выполняются ${rows.filter(r=>r.end===null).length} попытки.`;detail='Новые настройки не меняют этот запрос.';}
 else if(job.cancel_requested||job.cleared){heading='Запрос отменён пользователем.';detail='Уже принятый сервисом запрос может учитываться в квоте.';}
 else if(job.error){heading=budget?'Ответ не получен: исчерпан бюджет вызовов или подключений.':'Ответ не получен.';detail=budget?.message||job.error;}
 else if(winner){heading=`Ответила ${shortModel(winner.model_id)} за ${(duration/1000).toFixed(2)} с.`;detail=winning&&winning.start>0?`${events.some(e=>e.kind==='hedge_start'&&e.node_id===winning.node&&e.attempt?.model_id===winner.model_id)?'Запущена как резерв':'Победившая попытка началась'} на +${(winning.start/1000).toFixed(2)} с.`:'Первая успешная попытка дала принятый ответ.';}
 else if(events.length){heading='Маршрут завершён.';}
 return `<section class="run-summary"><strong>${esc(heading)}</strong><p>${esc(detail)}</p><div class="run-facts"><span>Вызовы: ${calls}${max?' / '+max:''}</span>${repeats?`<span>Повторных вызовов той же модели: ${repeats}</span>`:''}${trace?.config_revision?`<span>Конфигурация r${trace.config_revision}</span>`:''}</div><div class="summary-actions"><button class="text-button" data-insight-action="budget" data-pipeline-id="${esc(trace?.pipeline_id||'')}">Бюджет</button>${winning?`<button class="text-button" data-insight-action="stage" data-node-id="${esc(winning.node||'')}" data-pipeline-id="${esc(trace?.pipeline_id||'')}">Настройки этапа</button>`:''}${rows.length?'<button class="text-button" data-insight-action="keys">Подключения и ключи</button>':''}</div></section>`;
}
function executionTimelineMarkup(trace,job={}){
 const rows=attemptRows(trace);if(!rows.length)return '';
 const end=Math.max(1,finite(job.duration_ms),...rows.map(r=>r.end??r.start+1));
 return `<div class="execution-timeline"><div class="timeline-axis"><span>0 с</span><span>${(end/1000).toFixed(2)} с</span></div>${rows.map(row=>{
  const duration=(row.end??end)-row.start,label=row.outcome==='cancelled'?cancellationLabel(row,trace,job):row.outcome?outcomeNames[row.outcome]||row.outcome:'Выполняется',winner=trace?.winner?.id===row.attempt.id;
  return `<div class="timeline-row"><div class="timeline-model"><strong>${esc(shortModel(row.attempt.model_id))}</strong><small>${esc(row.attempt.transport||'')} · ${esc(row.attempt.credential_alias||row.attempt.credential_id||'без ключа')}</small></div><div class="timeline-track" title="${esc(label)} · +${(row.start/1000).toFixed(2)} с"><span class="timeline-bar ${winner?'winner':row.outcome==='cancelled'?'cancelled':row.outcome&&row.outcome!=='success'?'failed':''}" style="left:${row.start/end*100}%;width:${Math.max(.5,duration/end*100)}%"></span>${row.first!==null?`<i class="first-token-mark" style="left:${row.first/end*100}%" title="Первый текст +${(row.first/1000).toFixed(2)} с"></i>`:''}</div><span class="timeline-outcome">${esc(winner?'Принят':label)}</span></div>`;
 }).join('')}<p class="timeline-legend">Полоса — время попытки; отметка — первый измеренный текст. У buffered OpenCode отметки нет.</p></div>`;
}
function bindRunInsightActions(root){
 $$('[data-insight-action]',root).forEach(el=>el.onclick=guarded(async()=>{
  const action=el.dataset.insightAction;
  if(action==='keys'){await openSettingTarget({page:'providers',field:'providerSearch'});return;}
  const id=el.dataset.pipelineId,n=el.dataset.nodeId?.replace(/\/vision$/,'');
  if(id&&!state.config.pipelines.some(p=>p.id===id)){notify('Этот пайплайн больше не сохранён. История осталась доступна.');return;}
  await openSettingTarget({page:'pipelines',pipelineID:id,nodeID:n,tab:action==='budget'?'budget':'models',field:action==='budget'?'bCalls':null});
 }));
}
function renderLiveRun(){
 const root=$('#liveRun');if(!root)return;const j=state.job;
 if(!j?.id){root.innerHTML='<p class="note">Запусков в этом сеансе ещё нет.</p>';return;}
 const tr=j.trace||{events:[]},journalOpen=!!$('.full-trace',root)?.open;
 root.innerHTML=`<h3>${j.purpose==='connection_test'?'Проверка подключения':'Последний запуск'}</h3>${runSummaryMarkup(tr,j)}${executionTimelineMarkup(tr,j)}<details class="full-trace"><summary>Полный журнал · ${(tr.events||[]).length} событий</summary>${traceTable(tr.events||[])}</details>`;
 if(journalOpen&&$('.full-trace',root))$('.full-trace',root).open=true;bindRunInsightActions(root);
}
async function loadHistory(){
 const traces=await api('/api/traces'),root=$('#history');if(!root)return;
 root.innerHTML=(traces||[]).slice(0,20).map(t=>{const duration=Math.max(0,Date.parse(t.finished_at)-Date.parse(t.started_at)),last=(t.events||[]).findLast(e=>e.kind==='attempt_end');const job={duration_ms:duration,error:!t.winner&&last?.outcome!=='success'?(outcomeNames[last?.outcome]||'Запрос завершён без ответа'):''};return `<details class="history-card"><summary>${esc(t.pipeline_name||t.pipeline_id)} · ${esc(new Date(t.started_at).toLocaleTimeString())} · ${esc(shortModel(t.winner?.model_id||'без ответа'))}</summary>${runSummaryMarkup(t,job)}${executionTimelineMarkup(t,job)}<details class="full-trace"><summary>Полный журнал</summary>${traceTable(t.events||[])}</details></details>`;}).join('')||'<p class="note">История текущего сеанса пуста.</p>';
 bindRunInsightActions(root);
}
