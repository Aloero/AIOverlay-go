'use strict';

const sourceNames={manual:'Мой список',catalog:'Каталог подключений',manual_then_catalog:'Сначала список, затем каталог'};
const skipNames={not_configured:'нет подключения / ключа',not_free:'не FREE',needs_vision:'не принимает изображение',needs_audio:'нет AUDIO route',transcription_only:'только расшифровка',text_only_filter:'не TEXT ONLY',quality_floor:'ниже порога качества',disabled:'отключены',non_text_output:'нет текстового ответа',unreachable:'этап недостижим от включённых входов'};

function renderPoolInspector(n) {
 const pool=n.model_pool,transcribe=pool.task==='transcribe',decision=n.type==='decision';
 const taskLabel=transcribe?'AUDIO → TEXT':decision?'РЕШЕНИЕ О РЕАКЦИИ':'ОТВЕТ / ОБРАБОТКА';
 const contextMode=n.context_mode||(n.context_id?'named':'trigger'),contexts=contextOptions(pipe(),'Выберите context');
 const contextFields=selectField('Контекст этого AI-этапа','nodeContextMode',{trigger:'Наследовать от команды / источника',none:'Без local context',named:'Выбрать конкретный context'},contextMode)+(contextMode==='named'?selectField('Использовать context','nodeContextID',contexts,n.context_id||''):'');
 const resultRole=!transcribe&&!decision?selectField('Роль результата','poolResultRole',{'':'Обычный ответ / промежуточный текст','vision.text':'vision.text','context.summary':'context.summary · derived data'},pool.result_name||''):'';
 $('#inspectorBody').innerHTML=`${field('Название этапа','nodeName',n.name)}${contextFields}${resultRole}<div class="task-contract"><span>ЗАДАЧА</span><b>${taskLabel}</b><small>${transcribe?'Результат становится {{audio.text}}. Пустая корректная расшифровка не требует fallback.':decision?'Служебный результат: no_action / respond / need_screen. JSON решения не публикуется как ответ.':'Вход определяется графом и контекстом; несовместимые media route исключаются.'}</small></div>
 ${!transcribe&&!decision?`<label class="switch stage-switch"><input type="checkbox" id="enableAudioPreprocessor" ${pool.audio_preprocessor?'checked':''}>AUDIO → STT → {{audio.text}}</label><label class="switch stage-switch"><input type="checkbox" id="enableVision" ${pool.vision_preprocessor?'checked':''}>IMAGE → VLM → {{vision.text}}</label>`:''}<div id="audioPreprocessorPool"></div><div id="visionPool"></div><div id="solverSource"></div><div class="section-line"><h4>ПОРЯДОК ЗАПУСКА</h4></div>
 <div id="dispatchControls"></div><div id="poolPlan" class="pool-plan"></div>
 <div class="section-line"><h4>${pool.selection?.mode&&pool.selection.mode!=='manual'?'СПИСОК + КАТАЛОГ (СОХРАНЁННЫЙ РЕЖИМ)':'МОЙ СПИСОК'}</h4></div><div id="solverModels"></div><button class="add-model" id="addSolverModel">+ Выбрать совместимую модель</button><button class="add-model" id="seedSolverModel">+ Добавить доступные модели</button>
 ${outgoing(n.id)}`;
 bindValue('nodeName',n,'name',false,renderGraph);
 $('#nodeContextMode').onchange=e=>{n.context_mode=e.target.value;if(n.context_mode!=='named')delete n.context_id;dirty();renderGraph();renderInspector();};
 if($('#nodeContextID'))$('#nodeContextID').onchange=e=>{n.context_id=e.target.value;dirty();renderGraph();renderInspector();};
 if($('#poolResultRole'))$('#poolResultRole').onchange=e=>{pool.result_name=e.target.value;dirty();renderGraph();renderInspector();};
 if(transcribe){pool.selection||={mode:'catalog',provider_ids:[],free_only:false,modality:'audio',default_quality_score:0};pool.selection.modality='audio';pool.result_name='audio.text';}
 renderSourceControls($('#solverSource'),pool,'source',false);
 renderSlots($('#solverModels'),pool,false);
 $('#addSolverModel').onclick=()=>openCatalog(pool,false,{modality:transcribe?'audio':pool.selection?.modality||'auto'});
 $('#seedSolverModel').onclick=guarded(async()=>{const added=await seedPoolFromUniverse(pool,transcribe?'audio':null);if(added){dirty();renderInspector();refreshPoolPlan(pool);}notify(added?('Добавлено моделей: '+added):'Нет доступных моделей — зарегистрируйте провайдера на странице «Модели».');});
 if($('#enableAudioPreprocessor'))$('#enableAudioPreprocessor').onchange=e=>{if(e.target.checked)pool.audio_preprocessor=defaultAudioPreprocessor();else delete pool.audio_preprocessor;dirty();renderGraph();renderInspector();};
 if($('#enableVision'))$('#enableVision').onchange=e=>{if(e.target.checked)pool.vision_preprocessor=defaultVisionPool();else delete pool.vision_preprocessor;dirty();renderGraph();renderInspector();};
 if(pool.audio_preprocessor) {
  const audio=pool.audio_preprocessor;audio.task='transcribe';audio.result_name='audio.text';
  $('#audioPreprocessorPool').innerHTML=`<div class="vision-section"><p class="eyebrow">01 / AUDIO → TEXT</p><label class="switch"><input type="checkbox" id="audioOptional" ${pool.audio_preprocessor_optional?'checked':''}>Не блокировать solver при ошибке STT</label><div id="audioSource"></div><div id="audioModels"></div><button class="add-model" id="addAudioModel">+ Модель STT</button><button class="add-model" id="seedAudioModel">+ Доступные AUDIO-модели</button>${scheduleFields(audio,'audioPre')}<button class="button small" id="audioPlan">Очередь STT</button><div class="stage-output">↓ {{audio.text}}</div></div>`;
  renderSourceControls($('#audioSource'),audio,'audioSource',false);renderSlots($('#audioModels'),audio,false);
  $('#addAudioModel').onclick=()=>openCatalog(audio,false,{modality:'audio'});
  $('#seedAudioModel').onclick=guarded(async()=>{const added=await seedPoolFromUniverse(audio,'audio');if(added)dirty();renderInspector();notify(added?('Добавлено моделей: '+added):'Готовых AUDIO-моделей нет — добавьте local STT или модель с AUDIO вручную.');});
  $('#audioOptional').onchange=e=>{pool.audio_preprocessor_optional=e.target.checked;dirty();};
  bindSchedule(audio,'audioPre',()=>{renderGraph();renderInspector();});
  $('#audioPlan').onclick=guarded(()=>showPoolPlan(audio,false));
 }
 if(pool.vision_preprocessor) {
  const vision=pool.vision_preprocessor;
  $('#visionPool').innerHTML=`<div class="vision-section"><p class="eyebrow">${pool.audio_preprocessor?'02':'01'} / ЗРЕНИЕ</p><label class="switch"><input type="checkbox" id="visionOptional" ${pool.vision_preprocessor_optional?'checked':''}>Не блокировать solver при ошибке VLM</label><div id="visionSource"></div><div id="visionModels"></div><button class="add-model" id="addVisionModel">+ Модель со зрением</button>${scheduleFields(vision,'vision')}<button class="button small" id="visionPlan">Очередь VLM</button><button class="add-model" id="seedVisionModel">+ Доступные IMAGE-модели</button><div class="stage-output">↓ {{vision.text}}</div><p class="eyebrow">02 / РЕШЕНИЕ</p></div>`;
  renderSourceControls($('#visionSource'),vision,'visionSource',true);renderSlots($('#visionModels'),vision,true);
  $('#addVisionModel').onclick=()=>openCatalog(vision,true);
  $('#seedVisionModel').onclick=guarded(async()=>{const added=await seedPoolFromUniverse(vision,'image');if(added)dirty();renderInspector();notify(added?('Добавлено моделей: '+added):'Готовых IMAGE-моделей нет — добавьте их вручную.');});$('#visionOptional').onchange=e=>{pool.vision_preprocessor_optional=e.target.checked;dirty();};
  bindSchedule(vision,'vision',()=>{renderGraph();renderInspector();});
  $('#visionPlan').onclick=guarded(()=>showPoolPlan(vision,true));
 }
 renderDispatchControls(pool);refreshPoolPlan(pool);bindOutgoing();
}

function renderSourceControls(root,pool,prefix,isVision) {
 const mode=pool.selection?.mode||'manual';
 if(mode==='manual'){root.innerHTML='';return;}
 root.innerHTML=`<p class="note">Список работает поверх каталога: этап может обратиться к доступным моделям подключений. Провайдеров и модели настраивать на странице «Модели». <button class="text-button" id="${prefix}ToManual">Только мой список</button></p>`;
 $('#'+prefix+'ToManual').onclick=guarded(async()=>{
  const added=await seedPoolFromUniverse(pool,isVision?'image':pool.task==='transcribe'?'audio':null);
  pool.selection.mode='manual';pool.selection.free_only=false;dirty();renderGraph();renderInspector();
  notify(added?('Режим каталога выключен; добавлено моделей: '+added):'Режим каталога выключен. Готовых моделей не найдено — список можно собрать на странице «Модели».');
 });
}
function renderDispatchControls(pool) {
 $('#dispatchControls').innerHTML=scheduleFields(pool,'pool');
 bindSchedule(pool,'pool',()=>{renderGraph();renderDispatchControls(pool);refreshPoolPlan(pool);});
}
