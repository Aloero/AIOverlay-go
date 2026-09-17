'use strict';

const limitSourceNames={model:'задано для модели',pipeline:'наследуется от пайплайна',provider:'наследуется от подключения',default:'стандарт приложения'};
function inheritedSlotField(label,index,key,value){
 const explicit=Number(value)>0;
 return `<div class="field inheritance-field"><label>${esc(label)}</label><span class="effective-value" data-effective="${index},${key}">Загрузка действующего значения…</span><input type="number" min="0" max="600000" data-mfield="${index},${key}" value="${finite(value)}" ${explicit?'':'hidden'}><button class="text-button inherit-toggle" data-inherit="${index},${key}">${explicit?'Вернуть наследование':'Задать для этой модели'}</button></div>`;
}
function renderSlots(root,pool,isVision){
 root.innerHTML=pool.models.map((m,i)=>{
  const meta=modelMeta(m),tags=meta?(meta.free?tag('FREE','free'):'')+tag(meta.input_modalities.includes('image')?'IMAGE':'TEXT ONLY',meta.input_modalities.includes('image')?'image':'text'):tag('ПО ID','manual');
  return `<div class="pool-card" data-slot="${esc(m.id)}" draggable="true" data-slot-index="${i}"><div class="slot-header"><input type="checkbox" data-enabled="${i}" ${m.enabled?'checked':''} aria-label="Использовать ${esc(m.model_id)}"><div class="slot-name">${esc(shortModel(m.model_id))}<small>${esc(provider(m.provider_id)?.label||m.provider_id)} · ${esc(routeLabel(provider(m.provider_id)))}</small></div><div class="slot-actions"><button class="icon-button" data-move="${i},-1" title="Выше" ${i===0?'disabled':''}>↑</button><button class="icon-button" data-move="${i},1" title="Ниже" ${i===pool.models.length-1?'disabled':''}>↓</button><button class="icon-button" data-remove="${i}" title="${pool.selection?.mode&&pool.selection.mode!=='manual'?'Убрать приоритет: модель может остаться в каталоге':'Удалить'}">×</button></div></div><div class="tags slot-tags">${tags}${m.quality_score<pool.quality_floor?tag('НИЖЕ ПОРОГА','warn'):''}</div><details data-slot-details="${i}"><summary>Параметры и наследование</summary><div class="field"><label>Model ID</label><input class="mono" data-mfield="${i},model_id" value="${esc(m.model_id)}"></div><div class="form-row"><div class="field"><label>Приоритет</label><input type="number" data-mfield="${i},priority" value="${finite(m.priority)}"></div><div class="field"><label>Качество · ваша оценка</label><input type="number" min="0" max="100" data-mfield="${i},quality_score" value="${finite(m.quality_score)}"></div></div>${inheritedSlotField('Полный ответ, мс',i,'timeout_ms',m.timeout_ms)}${provider(m.provider_id)?.kind==='embedded_opencode'?'<div class="field"><label>Первый текст</label><span class="effective-value">Не измеряется: OpenCode возвращает готовый ответ.</span></div>':inheritedSlotField('Первый текст, мс',i,'first_token_timeout_ms',m.first_token_timeout_ms)}<div class="form-row"><div class="field"><label>Попыток на ключ</label><input type="number" min="1" max="100" data-mfield="${i},max_attempts" value="${finite(m.max_attempts,1)}"></div><div class="field"><label>Вес случайного выбора</label><input type="number" step="0.1" min="0" data-mfield="${i},weight" value="${finite(m.weight,1)}"></div></div><p class="note" data-slot-limit-note="${i}"></p></details></div>`;
 }).join('');
 const effective=new Map(),requests=new Map(),generation={};root._limitsGeneration=generation;
 async function loadLimits(i){
  const m=pool.models[i];if(!m)return;const p=provider(m.provider_id),serial=(requests.get(i)||0)+1;requests.set(i,serial);
  const reply=await api('/api/limits/preview','POST',{slot:m,budget:pipe().budget,defaults:p?.request_defaults||{}});if(!root.isConnected||root._limitsGeneration!==generation||requests.get(i)!==serial||pool.models[i]!==m)return;effective.set(i,reply.limits);
  for(const [key,limit]of [['timeout_ms','response_timeout_ms'],['first_token_timeout_ms','first_token_timeout_ms']]){
   const el=$$('[data-effective]',root).find(el=>el.dataset.effective===i+','+key);
   if(el)el.textContent=(reply.limits[limit]/1000)+' с · '+limitSourceNames[reply.sources[limit]];
  }
  const note=$$('[data-slot-limit-note]',root).find(el=>el.dataset.slotLimitNote===String(i));if(note)note.textContent=p?.kind==='embedded_opencode'?'OpenCode возвращает полный ответ. Первый токен и пауза стрима не измеряются; действует общий timeout ответа.':`Соединение ${reply.limits.connect_timeout_ms/1000} с · пауза стрима ${reply.limits.stream_idle_timeout_ms/1000} с · до ${reply.limits.max_output_tokens} токенов.`;
 }
 $$('[data-slot-details]',root).forEach(el=>el.ontoggle=()=>{if(el.open)guarded(()=>loadLimits(Number(el.dataset.slotDetails)))();});
 $$('[data-enabled]',root).forEach(e=>e.onchange=()=>{pool.models[Number(e.dataset.enabled)].enabled=e.checked;dirty();renderGraph();if(!isVision)refreshPoolPlan(pool);});
 $$('[data-remove]',root).forEach(b=>b.onclick=()=>{pool.models.splice(Number(b.dataset.remove),1);dirty();renderInspector();renderGraph();});
 function move(from,to){if(from===to||to<0||to>=pool.models.length)return;const [m]=pool.models.splice(from,1);pool.models.splice(to,0,m);pool.models.forEach((m,j)=>m.priority=100-j);dirty();renderInspector();renderGraph();}
 $$('[data-move]',root).forEach(b=>b.onclick=()=>{const [i,d]=b.dataset.move.split(',').map(Number);move(i,i+d);});
 $$('[data-mfield]',root).forEach(el=>{const assign=()=>{const [i,key]=el.dataset.mfield.split(','),m=pool.models[Number(i)],v=key==='model_id'?el.value.trim():finite(el.value);if(m[key]!==v){m[key]=v;dirty();}};el.oninput=assign;el.onchange=()=>{assign();renderGraph();guarded(()=>loadLimits(Number(el.dataset.mfield.split(',')[0])))();};});
 $$('[data-inherit]',root).forEach(el=>el.onclick=guarded(async()=>{
  const [raw,key]=el.dataset.inherit.split(','),i=Number(raw),m=pool.models[i];if(!m)return;if(!effective.has(i))await loadLimits(i);
  if(!root.isConnected||root._limitsGeneration!==generation||pool.models[i]!==m||!effective.has(i))return;
  m[key]=m[key]>0?0:effective.get(i)[key==='timeout_ms'?'response_timeout_ms':key];dirty();
  renderSlots(root,pool,isVision);const details=$$('[data-slot-details]',root).find(e=>e.dataset.slotDetails===String(i));if(details)details.open=true;renderGraph();
 }));
 let dragging=null;
 $$('[data-slot-index]',root).forEach(el=>{
  el.ondragstart=e=>{if(e.target.closest('input,button,textarea')){e.preventDefault();return;}dragging=Number(el.dataset.slotIndex);e.dataTransfer.setData('text/plain','model-slot');e.dataTransfer.effectAllowed='move';};
  el.ondragover=e=>{if(dragging!==null){e.preventDefault();e.dataTransfer.dropEffect='move';}};
  el.ondrop=e=>{if(dragging===null)return;e.preventDefault();move(dragging,Number(el.dataset.slotIndex));dragging=null;};el.ondragend=()=>dragging=null;
 });
}
async function annotatePipelineInheritance(){
 const root=$('#inspectorBody');if(!root)return;
 const defaults={connect_timeout_ms:3000,first_token_timeout_ms:8000,response_timeout_ms:15000,stream_idle_timeout_ms:5000};
 for(const [id,key]of [['bConnect','connect_timeout_ms'],['bFirst','first_token_timeout_ms'],['bResponse','response_timeout_ms'],['bIdle','stream_idle_timeout_ms']]){
  const input=$('#'+id);if(!input)continue;const value=pipe().budget[key],line=document.createElement('span');line.className='effective-value';line.textContent=value>0?value/1000+' с · задано для пайплайна':'Наследуется от подключения; при отсутствии — стандарт приложения';input.after(line);input.hidden=!(value>0);
  const button=document.createElement('button');button.className='text-button inherit-toggle';button.textContent=value>0?'Вернуть наследование':'Задать для пайплайна';input.parentElement.append(button);
  const update=()=>{const current=Number(input.value);line.textContent=current>0?current/1000+' с · задано для пайплайна':'Наследуется от подключения; при отсутствии — стандарт приложения';button.textContent=current>0?'Вернуть наследование':'Задать для пайплайна';};
  input.addEventListener('input',update);input.addEventListener('change',update);
  button.onclick=()=>{pipe().budget[key]=pipe().budget[key]>0?0:defaults[key];dirty();renderBudgetInspector();};
 }
 const detail=document.createElement('details');detail.className='provider-limit-preview';detail.innerHTML='<summary>Действующие значения без override модели</summary><div class="inherited-connections"></div>';root.append(detail);
 detail.ontoggle=guarded(async()=>{
  if(!detail.open)return;const rows=[];
  for(const p of state.config.providers.filter(p=>p.enabled).slice(0,24)){const reply=await api('/api/limits/preview','POST',{slot:{},budget:pipe().budget,defaults:p.request_defaults});if(!detail.isConnected)return;rows.push(`<div><strong>${esc(p.label)}</strong><small>Первый текст: ${p.kind==='embedded_opencode'?'не измеряется':reply.limits.first_token_timeout_ms/1000+' с'} · ответ: ${reply.limits.response_timeout_ms/1000} с · соединение: ${reply.limits.connect_timeout_ms/1000} с</small></div>`);}
  $('.inherited-connections',detail).innerHTML=rows.join('');
 });
}
