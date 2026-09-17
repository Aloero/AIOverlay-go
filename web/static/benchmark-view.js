'use strict';

function renderRuns(){
 $('#page').innerHTML=`<div class="page-head"><h1>Запуски</h1><button class="button danger" id="stopTraceRun">Отменить</button></div><div class="scroll-page"><section class="card benchmark-panel"><div class="section-line"><h3>Synthetic benchmark</h3><div class="tool-group"><label for="benchmarkRuns">Повторов</label><input id="benchmarkRuns" type="number" min="1" max="10" value="3"><button class="button" id="syntheticRun">Запустить тест</button></div></div><p class="note">Фиксированный снимок Q1–Q4. Проверяется однозначный ответ Q4 / 210; свободная формулировка может потребовать ручной оценки. Пайплайн не изменяется.</p><div id="benchmarkResult"></div></section><div id="liveRun"></div><div class="section-line"><h4>ИСТОРИЯ</h4><button class="button small subtle" id="refreshHistory">Обновить</button></div><div id="history"></div></div>`;
 $('#syntheticRun').onclick=guarded(async()=>{
  if(state.dirty){await saveConfig();if(state.dirty)throw new Error('Сохраните новые изменения перед тестом.');}
  const runs=Number($('#benchmarkRuns').value);if(!Number.isInteger(runs)||runs<1||runs>10)throw new Error('Укажите от 1 до 10 повторов.');
  const button=$('#syntheticRun');button.disabled=true;
  try{await api('/api/benchmark','POST',{pipeline_id:pipe().id,runs});state.job=await api('/api/job');renderBenchmarkResult();}finally{if(button.isConnected)button.disabled=!!state.job?.running;}
 });
 $('#stopTraceRun').onclick=guarded(stopRun);$('#refreshHistory').onclick=guarded(loadHistory);
 renderLiveRun();renderBenchmarkResult();guarded(loadHistory)();
}
function renderBenchmarkResult(){
 const root=$('#benchmarkResult');if(!root)return;
 const job=state.job,b=job?.benchmark;
 $('#syntheticRun').disabled=!!job?.running;
 if(!b){root.textContent=job?.running?'Выполняется запрос…':'';return;}
 const label={correct:'Верно',incorrect:'Неверно',unverified:'Проверить вручную',error:'Ошибка'};
 root.innerHTML=`<div class="plan-totals"><span>${b.total} / ${b.requested}${b.cancelled?' · отменён':''}</span><span>Медиана ${(b.median_ms/1000).toFixed(2)} с</span><span>Успех ${(b.success_rate*100).toFixed(0)}%</span><span>Верно ${b.correct}; вручную ${b.unverified}</span></div><div class="table-wrap"><table><thead><tr><th>№</th><th>Победитель</th><th>Ответ</th><th>Время</th><th>TTFT</th><th>TPS</th></tr></thead><tbody>${(b.runs||[]).map(r=>`<tr><td>${r.run}</td><td class="mono">${esc(r.winner?.provider_id||'')}<br>${esc(r.winner?.model_id||'—')}</td><td><details><summary>${esc(label[r.verdict]||r.verdict)}</summary><pre class="answer-preview">${esc(r.text)}</pre></details></td><td>${(r.duration_ms/1000).toFixed(2)} с</td><td>${r.ttft_ms?(r.ttft_ms/1000).toFixed(2)+' с':'—'}</td><td>${r.tps?r.tps.toFixed(1):'—'}</td></tr>`).join('')}</tbody></table></div>`;
}
