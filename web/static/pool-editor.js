'use strict';

const sourceNames={manual:'Мой список',catalog:'Каталог подключений',manual_then_catalog:'Сначала список, затем каталог'};
const skipNames={not_configured:'нет подключения / ключа',not_free:'не FREE',needs_vision:'не принимает изображение',needs_audio:'нет AUDIO route',transcription_only:'только расшифровка',text_only_filter:'не TEXT ONLY',quality_floor:'ниже порога качества',disabled:'отключены',non_text_output:'нет текстового ответа',unreachable:'этап недостижим от включённых входов'};

function renderPoolInspector(n) {
 const pool=n.model_pool,transcribe=pool.task==='transcribe',decision=n.type==='decision';
 const taskLabel=transcribe?'AUDIO → TEXT':decision?'РЕШЕНИЕ О РЕАКЦИИ':'ОТВЕТ / ОБРАБОТКА';
 const contextMode=n.context_mode||(n.context_id?'named':'trigger'),contexts=contextOptions(pipe(),'Выберите context');
 const contextFields=selectField('Контекст этого AI-этапа','nodeContextMode',{trigger:'Наследовать от команды / источника',none:'Без local context',named:'Выбрать конкретный context'},contextMode)+(contextMode==='named'?selectField('Использовать context','nodeContextID',contexts,n.context_id||''):'');
 $('#inspectorBody').innerHTML=`${field('Название этапа','nodeName',n.name)}${contextFields}<div class="task-contract"><span>ЗАДАЧА</span><b>${taskLabel}</b><small>${transcribe?'Результат становится {{audio.text}}. Пустая корректная расшифровка не требует fallback.':decision?'Служебный результат: no_action / respond / need_screen. JSON решения не публикуется как ответ.':'Вход определяется графом и контекстом; несовместимые media route исключаются.'}</small></div>
 ${!transcribe&&!decision?`<label class="switch stage-switch"><input type="checkbox" id="enableVision" ${pool.vision_preprocessor?'checked':''}>Зрение → текстовая модель</label>`:''}<div id="visionPool"></div><div id="solverSource"></div><div class="section-line"><h4>ПОРЯДОК ЗАПУСКА</h4></div>
 <div id="dispatchControls"></div><div id="poolPlan" class="pool-plan"></div>
 <div class="section-line"><h4>${pool.selection?.mode&&pool.selection.mode!=='manual'?'СНАЧАЛА ПРОБОВАТЬ':'МОЙ СПИСОК'}</h4></div><div id="solverModels"></div><button class="add-model" id="addSolverModel">+ Выбрать совместимую модель</button>
 ${outgoing(n.id)}`;
 bindValue('nodeName',n,'name',false,renderGraph);
 $('#nodeContextMode').onchange=e=>{n.context_mode=e.target.value;if(n.context_mode!=='named')delete n.context_id;dirty();renderGraph();renderInspector();};
 if($('#nodeContextID'))$('#nodeContextID').onchange=e=>{n.context_id=e.target.value;dirty();renderGraph();renderInspector();};
 if(transcribe){pool.selection||={mode:'catalog',provider_ids:[],free_only:false,modality:'audio',default_quality_score:0};pool.selection.modality='audio';pool.result_name='audio.text';}
 renderSourceControls($('#solverSource'),pool,'source',false);
 renderSlots($('#solverModels'),pool,false);
 $('#addSolverModel').onclick=()=>openCatalog(pool,false,{modality:transcribe?'audio':pool.selection?.modality||'auto'});
 if($('#enableVision'))$('#enableVision').onchange=e=>{if(e.target.checked)pool.vision_preprocessor=defaultVisionPool();else delete pool.vision_preprocessor;dirty();renderGraph();renderInspector();};
 if(pool.vision_preprocessor) {
  const vision=pool.vision_preprocessor;
  $('#visionPool').innerHTML=`<div class="vision-section"><p class="eyebrow">01 / ЗРЕНИЕ</p><div id="visionSource"></div><div id="visionModels"></div><button class="add-model" id="addVisionModel">+ Модель со зрением</button>${scheduleFields(vision,'vision')}<button class="button small" id="visionPlan">Очередь VLM</button><div class="stage-output">↓ {{vision.text}}</div><p class="eyebrow">02 / РЕШЕНИЕ</p></div>`;
  renderSourceControls($('#visionSource'),vision,'visionSource',true);renderSlots($('#visionModels'),vision,true);
  $('#addVisionModel').onclick=()=>openCatalog(vision,true);
  bindSchedule(vision,'vision',()=>{renderGraph();renderInspector();});
  $('#visionPlan').onclick=guarded(()=>showPoolPlan(vision,true));
 }
 renderDispatchControls(pool);refreshPoolPlan(pool);bindOutgoing();
}

function renderSourceControls(root,pool,prefix,isVision) {
 const mode=pool.selection?.mode||'manual',auto=mode!=='manual';
 root.innerHTML=selectField(isVision?'Модели VLM':'Источник моделей',prefix+'Mode',sourceNames,mode)+
 (auto?`<div class="source-settings"><label class="switch"><input type="checkbox" id="${prefix}Free" ${pool.selection.free_only?'checked':''}>Только FREE</label>${selectField('Вход модели',prefix+'Modality',isVision?{image:'IMAGE'}:pool.task==='transcribe'?{audio:'AUDIO → TEXT'}:{auto:'По входу этапа',image:'Только IMAGE',audio:'Только AUDIO',audio_image:'AUDIO + IMAGE',text:'Только TEXT ONLY'},isVision?'image':pool.selection.modality||'auto')}<details><summary>Подключения и качество</summary><div class="provider-scope" id="${prefix}Providers"></div><div class="form-row">${field('Порог качества',prefix+'Floor',pool.quality_floor,'number')}${field('Оценка остальных',prefix+'Quality',pool.selection.default_quality_score||0,'number','В Models.dev нет оценки качества. 0 означает отсутствие вашей оценки. Порог выше 0 исключает такие модели.')}</div></details></div>`:'');
 $('#'+prefix+'Mode').onchange=e=>{
  const next=e.target.value;
  pool.selection||={mode:'manual',provider_ids:[],free_only:true,modality:isVision?'image':'auto',default_quality_score:0};
  pool.selection.mode=next;dirty();renderGraph();renderInspector();
 };
 if(!auto)return;
 $('#'+prefix+'Free').onchange=e=>{pool.selection.free_only=e.target.checked;dirty();renderGraph();if(isVision)renderInspector();else refreshPoolPlan(pool);};
 $('#'+prefix+'Modality').onchange=e=>{
  pool.selection.modality=e.target.value;
  if(!isVision && pool.task!=='transcribe' && e.target.value==='text'&&!pool.vision_preprocessor)pool.vision_preprocessor=defaultVisionPool();
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
 $('#dispatchControls').innerHTML=scheduleFields(pool,'pool');
 bindSchedule(pool,'pool',()=>{renderGraph();renderDispatchControls(pool);refreshPoolPlan(pool);});
}
