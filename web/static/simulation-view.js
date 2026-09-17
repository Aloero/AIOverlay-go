'use strict';

async function openSimulation(){
 if($('#dialog').open&&!await closeModal())return;
 const pipeline=copy(pipe()),config=copy(state.config),options=[];
 for(const n of pipeline.nodes.filter(n=>n.model_pool)){
  const stages=[{id:n.id,name:n.name,pool:n.model_pool,image:!n.model_pool.vision_preprocessor}];
  if(n.model_pool.vision_preprocessor)stages.unshift({id:n.id+'/vision',name:n.name+' / Извлечение',pool:n.model_pool.vision_preprocessor,image:true});
  for(const stage of stages){const plan=await api('/api/pool/plan','POST',{config,pool:stage.pool,image:stage.image,pipeline_id:pipeline.id,node_id:stage.id});for(const m of plan.candidates.slice(0,100))options.push({node_id:stage.id,provider_id:m.provider_id,model_id:m.model_id,label:stage.name+' · '+shortModel(m.model_id)});}
 }
 if(!options.length)throw new Error('Сначала настройте хотя бы одну подходящую модель. Симуляция использует те же правила отбора.');
 const triggers=[];
 for(const command of pipeline.commands||[])triggers.push({id:command.id,label:'Команда · '+(command.name||command.action||command.id)});
 for(const source of (pipeline.sources||[]).filter(source=>source.enabled))triggers.push({id:source.id,label:'Источник · '+(source.name||source.kind||source.id)});
 if(!triggers.length)triggers.push({id:'',label:'Legacy entry · synthetic screen'});
 let triggerID=triggers.find(trigger=>(pipeline.commands||[]).some(command=>command.id===trigger.id&&command.action==='analyze'))?.id||triggers[0].id;
 let contextText='';
 let scripts=[{target:0,outcome:'model_unavailable',duration_ms:300,first_token_ms:0,attempt:1,text:'42'}];
 const outcomeChoices={success:'Успешный ответ',auth_failed:'Ошибка ключа',rate_limited:'429 / лимит частоты',quota_exceeded:'Квота исчерпана',timeout:'Таймаут ответа',first_token_timeout:'Нет первого текста',stream_idle_timeout:'Пауза стрима',network_error:'Ошибка сети',model_unavailable:'Модель недоступна',region_restricted:'Ограничение региона',provider_unavailable:'Провайдер недоступен',invalid_response:'Некорректный ответ',validation_failed:'Проверка не пройдена',cancelled:'Отмена'};
 modal('Симуляция маршрута',`<p class="simulation-notice">Условные исходы, не прогноз ответа. Нет реального снимка, аудиоустройства, сети и расхода API-квоты. Command/source задаёт ту же entry + intent семантику, что runtime. Неуказанные model attempts: успех через 1 с, текст «42».</p><div class="form-row">${selectField('Событие / trigger','simTrigger',Object.fromEntries(triggers.map(trigger=>[trigger.id,trigger.label])),triggerID)}${field('Synthetic context','simContext',contextText,'text','Не отправляется модели. Используется только виртуальным run.')}</div><div id="simulationRows"></div><button class="button small" id="addSimulationRow">+ Условие</button><div class="dialog-footer"><span>Черновик: ${esc(pipeline.name)} · ≤ ${pipeline.budget.max_model_calls} вызовов</span><button class="button primary" id="simulateNow">Рассчитать маршрут</button></div><div id="simulationReport" aria-live="polite"></div>`,'ВИРТУАЛЬНОЕ ВРЕМЯ · БЕЗ AI');
 $('#simTrigger').onchange=event=>{triggerID=event.target.value;};
 $('#simContext').onchange=event=>{contextText=event.target.value;};
 function renderRows(){
  $('#simulationRows').innerHTML=scripts.map((row,i)=>`<section class="simulation-row" data-simulation-row="${i}"><div class="section-line"><strong>Условие ${i+1}</strong><button class="icon-button" data-remove-simulation="${i}" aria-label="Удалить условие">×</button></div>${selectField('Модель и этап','simTarget'+i,Object.fromEntries(options.map((o,j)=>[j,o.label])),String(row.target))}<div class="form-row">${selectField('Исход','simOutcome'+i,outcomeChoices,row.outcome)}${field('Попытка модели (0 — все)','simAttempt'+i,row.attempt,'number')}</div><div class="form-row">${field('Завершение через, мс','simDuration'+i,row.duration_ms,'number')}${field('Первый текст через, мс','simFirst'+i,row.first_token_ms,'number')}</div>${field('Текст ответа / Decision JSON','simText'+i,row.text,'text','Для Decision используйте structured JSON, например action=no_action.')}</section>`).join('');
  scripts.forEach((row,i)=>{for(const [suffix,key,numeric]of [['Target','target',true],['Outcome','outcome',false],['Attempt','attempt',true],['Duration','duration_ms',true],['First','first_token_ms',true],['Text','text',false]])$('#sim'+suffix+i).onchange=e=>row[key]=numeric?finite(e.target.value):e.target.value;});
  $$('[data-remove-simulation]').forEach(b=>b.onclick=()=>{scripts.splice(Number(b.dataset.removeSimulation),1);renderRows();});$('#addSimulationRow').disabled=scripts.length>=16;
 }
 $('#addSimulationRow').onclick=()=>{scripts.push({target:Math.min(scripts.length,options.length-1),outcome:'success',duration_ms:1000,first_token_ms:200,attempt:1,text:'42'});renderRows();};
 $('#simulateNow').onclick=guarded(async()=>{
  document.activeElement?.dispatchEvent(new Event('change',{bubbles:true}));
  const button=$('#simulateNow'),root=$('#simulationReport');button.disabled=true;
  try{
   const outcomes=scripts.map(row=>({...options[row.target],outcome:row.outcome,attempt:row.attempt,duration_ms:row.duration_ms,first_token_ms:row.first_token_ms,text:row.text})).map(({label,...o})=>o);
   const report=await api('/api/simulation','POST',{config,pipeline_id:pipeline.id,trigger_id:triggerID,context_text:contextText,outcomes});if(!root.isConnected)return;
   const job={duration_ms:report.elapsed_ms,error:report.error};root.innerHTML=`<h3>Условный результат</h3>${runSummaryMarkup(report.trace,job)}<p class="note">Оставшиеся кандидаты после победы: ${report.remaining_candidates}. ${poolOrder(pipeline.nodes.find(n=>n.model_pool)?.model_pool||{})==='weighted'?'Весовой выбор условный.':''} Симуляция ограничена 256 вызовами и 10 000 шагами.</p>${executionTimelineMarkup(report.trace,job)}<details class="full-trace"><summary>Все условные события</summary>${traceTable(report.trace.events||[])}</details>`;bindRunInsightActions(root);
  }finally{if(button.isConnected)button.disabled=false;}
 });renderRows();
}
