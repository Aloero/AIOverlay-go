'use strict';

function attemptRows(trace){
 const rows=new Map();
 for(const e of trace?.events||[]){
  if(!e.attempt)continue;
  const a=e.attempt,key=a.id||[e.node_id,a.provider_id,a.model_id,a.credential_id].join('|');
  if(e.kind==='attempt_start')rows.set(key,{key,node:e.node_id,plan:e.plan_id,attempt:a,start:e.at_ms||0,end:null,first:null,outcome:null});
  const row=rows.get(key);if(!row)continue;
  if(e.kind==='first_token')row.first=e.at_ms;
  if(e.kind==='attempt_end'){
   Object.assign(row,{end:e.at_ms,outcome:e.outcome,duration:e.duration_ms,ttft:e.ttft_ms||0,tps:e.tps||0,tokens:e.output_tokens||0,reason:e.message||'',cancelReason:e.cancel_reason});
   if(row.first===null&&e.ttft_ms>0)row.first=row.start+e.ttft_ms;
  }
 }
 return [...rows.values()].sort((a,b)=>a.start-b.start);
}
function cancellationLabel(row,trace,job={}){
 const otherWinner=(trace?.events||[]).find(e=>e.kind==='winner'&&e.node_id===row.node&&e.attempt?.id!==row.attempt?.id&&e.at_ms>=row.start&&e.at_ms<=(row.end??Infinity));
 if(row.cancelReason==='race_lost'||row.reason==='race_lost'||otherWinner)return 'Остановлен после победы другой модели';
 if(row.cancelReason==='user'||job.cancel_requested||trace?.cancel_reason==='user')return 'Отменён пользователем';
 return 'Попытка остановлена';
}
const budgetReasonNames={model_calls:'Закончился общий бюджет вызовов.',pool_calls:'Закончился бюджет вызовов этапа.',keys:'Достигнут лимит разных ключей провайдера.',providers:'Достигнут лимит разных провайдеров.'};
function runSummaryMarkup(trace,job={}){
 const events=trace?.events||[],rows=attemptRows(trace),error=job.error||trace?.error||'',outcome=trace?.outcome;
 const winner=error||outcome&&outcome!=='success'?null:trace?.winner;
 const winning=rows.find(r=>r.attempt.id&&r.attempt.id===winner?.id),calls=rows.length,max=trace?.budget?.max_model_calls;
 const duration=finite(job.duration_ms,(trace?.finished_at&&trace?.started_at)?Date.parse(trace.finished_at)-Date.parse(trace.started_at):0);
 const budget=events.findLast(e=>e.kind==='budget_exhausted'),edge=events.findLast(e=>e.kind==='transition'&&e.outcome!=='success'&&e.edge_id);
 const failed=rows.findLast(r=>r.outcome&&r.outcome!=='success'&&r.outcome!=='cancelled'),relevant=failed||winning||rows.at(-1);
 const repeats=Math.max(0,calls-new Set(rows.map(r=>[r.plan||r.node,r.attempt.provider_id,r.attempt.model_id].join('/'))).size);
 const untried=events.filter(e=>e.kind==='pool_end'&&e.untried_models>0);
 let heading='Запрос ещё не запускался.',detail='';
 if(job.running){heading=job.phase==='preparing'?'Готовлю входные данные…':`Выполняются ${rows.filter(r=>r.end===null).length} попытки.`;detail='Новые настройки не меняют этот запрос.';}
 else if(job.cancel_requested||job.cleared||trace?.cancel_reason==='user'){heading='Запрос отменён пользователем.';detail='Уже принятый сервисом запрос может учитываться в квоте.';}
 else if(error||outcome&&outcome!=='success'){heading=budget?'Ответ не получен: исчерпан бюджет.':'Ответ не получен.';detail=budget?(budgetReasonNames[budget.budget_scope]||'Достигнут лимит выполнения.'):error||outcomeNames[outcome]||outcome;}
 else if(job.unknown_terminal){heading='Итог запуска не записан старой версией.';detail='Успех отдельной модели не подтверждает прохождение всего пайплайна.';}
 else if(winner){
  heading=`Ответила ${shortModel(winner.model_id)} за ${(duration/1000).toFixed(2)} с.`;
  detail=winning&&winning.start>0?`${events.some(e=>e.kind==='hedge_start'&&e.node_id===winning.node&&e.attempt?.model_id===winner.model_id)?'Запущена как резерв':'Победившая попытка началась'} на +${(winning.start/1000).toFixed(2)} с.`:'Первая успешная попытка дала принятый ответ.';
  const losers=rows.filter(r=>r.outcome==='cancelled'&&cancellationLabel(r,trace,job)==='Остановлен после победы другой модели');
  if(losers.length)detail+=' После принятия ответа остановлены: '+losers.map(r=>shortModel(r.attempt.model_id)).join(', ')+'.';
 }else if(outcome==='success'){heading='Пайплайн завершён.';}else if(events.length){heading='Маршрут завершён.';}
 const pid=esc(trace?.pipeline_id||''),node=trace?.failure_node_id||winning?.node||relevant?.node;
 return `<section class="run-summary"><strong>${esc(heading)}</strong><p>${esc(detail)}</p><div class="run-facts"><span>Вызовы: ${calls}${max?' / '+max:''}</span>${repeats?`<span>Повторных вызовов той же модели: ${repeats}</span>`:''}${trace?.config_revision?`<span>Конфигурация r${trace.config_revision}</span>`:''}</div>${untried.length?`<div class="untried-models">${untried.map(e=>`<p>На этапе ${esc(e.node_id)} не проверено ${e.untried_models} из ${e.candidate_count} моделей.</p>`).join('')}</div>`:''}<div class="summary-actions"><button class="text-button" data-insight-action="budget" data-pipeline-id="${pid}">Бюджет</button>${node?`<button class="text-button" data-insight-action="stage" data-node-id="${esc(node)}" data-pipeline-id="${pid}">Настройки этапа</button>`:''}${relevant?`<button class="text-button" data-insight-action="keys" data-provider-id="${esc(relevant.attempt.provider_id)}">Ключи ${esc(relevant.attempt.provider_id)}</button>`:''}${edge?`<button class="text-button" data-insight-action="edge" data-edge-id="${esc(edge.edge_id)}" data-pipeline-id="${pid}">Условие резервного перехода</button>`:''}</div></section>`;
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
  if(action==='keys'){await openSettingTarget({page:'providers',providerID:el.dataset.providerId,field:'providerSearch'});return;}
  const id=el.dataset.pipelineId,n=el.dataset.nodeId?.replace(/\/vision$/,'');
  if(id&&!state.config.pipelines.some(p=>p.id===id)){notify('Этот пайплайн больше не сохранён. История осталась доступна.');return;}
  await openSettingTarget({page:'pipelines',pipelineID:id,nodeID:n,edgeID:el.dataset.edgeId,tab:action==='budget'?'budget':'models',field:action==='budget'?'bCalls':null});
 }));
}
function renderLiveRun(){
 const root=$('#liveRun');if(!root)return;const j=state.job;
 if(!j?.id){root.innerHTML='<p class="note">Запусков в этом сеансе ещё нет.</p>';return;}
 const tr=j.trace||{events:[]},open=new Set($$('details',root).flatMap((el,i)=>el.open?[i]:[]));
 root.innerHTML=`<h3>${j.purpose==='connection_test'?'Проверка подключения':'Последний запуск'}</h3>${runSummaryMarkup(tr,j)}${executionTimelineMarkup(tr,j)}<details class="full-trace"><summary>Полный журнал · ${(tr.events||[]).length} событий</summary>${traceTable(tr.events||[])}</details>`;
 $$('details',root).forEach((el,i)=>{if(open.has(i))el.open=true;});bindRunInsightActions(root);
}
async function loadHistory(){
 const traces=await api('/api/traces'),root=$('#history');if(!root)return;
 root.innerHTML=(traces||[]).slice(0,20).map(t=>{
  const duration=Math.max(0,Date.parse(t.finished_at)-Date.parse(t.started_at));
  const job={duration_ms:duration,error:t.error||'',unknown_terminal:!t.outcome};
  const label=t.outcome&&t.outcome!=='success'?(outcomeNames[t.outcome]||t.outcome):shortModel(t.winner?.model_id||'без ответа');
  return `<details class="history-card"><summary>${esc(t.pipeline_name||t.pipeline_id)} · ${esc(new Date(t.started_at).toLocaleTimeString())} · ${esc(label)}</summary>${runSummaryMarkup(t,job)}${executionTimelineMarkup(t,job)}<details class="full-trace"><summary>Полный журнал</summary>${traceTable(t.events||[])}</details></details>`;
 }).join('')||'<p class="note">История текущего сеанса пуста.</p>';
 bindRunInsightActions(root);
}
