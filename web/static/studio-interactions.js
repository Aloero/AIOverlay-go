'use strict';

async function activatePipeline(id) {
 if(state.selectingPipeline)return;
 const p=state.config.pipelines.find(p=>p.id===id);
 if(!p?.enabled)throw new Error('Этот пайплайн отключён.');
 state.selectingPipeline=true;
 try {
  if(state.job?.running)throw new Error('Сначала завершите запрос. Его пайплайн не меняется во время выполнения.');
  if(state.dirty)await saveConfig();
  if(state.dirty)throw new Error('Остались новые изменения. Сохраните их перед переключением пайплайна.');
  if(state.config.active_pipeline_id!==id){
   await api('/api/pipelines/active','POST',{id});
   state.config.active_pipeline_id=id;acknowledgeActivation(id);
  }
  state.pipeline=id;state.node=null;state.edge=null;state.page='pipelines';renderPage();
 }finally{state.selectingPipeline=false;}
}

function restoreViewport() {
 const layout=state.config.control_center.layout?.[pipe().id];
 state.pan={x:finite(layout?.pan_x,20),y:finite(layout?.pan_y,20)};
 state.zoom=clamp(layout?.zoom??.8,.25,1.6);
}
function applyViewport() {
 const inner=$('#canvasInner');if(!inner)return;
 state.pan||={x:20,y:20};inner.style.transform=`translate(${state.pan.x}px, ${state.pan.y}px) scale(${state.zoom})`;
 $('#zoomLabel').textContent=Math.round(state.zoom*100)+'%';
 const canvas=$('#canvas');canvas.style.backgroundPosition=`${state.pan.x}px ${state.pan.y}px`;canvas.style.backgroundSize=`${20*state.zoom}px ${20*state.zoom}px`;
}
function rememberViewport(markDirty=true) {
 if(!markDirty)return; // Initial fit is visual, not an unsaved config change.
 state.config.control_center.layout||={};
 state.config.control_center.layout[pipe().id]={pan_x:state.pan.x,pan_y:state.pan.y,zoom:state.zoom};
 if(markDirty)dirty();
}
function zoomCanvas(next,x,y) {
 const canvas=$('#canvas');if(!canvas)return;
 const old=state.zoom;next=clamp(next,.25,1.6);
 x??=canvas.clientWidth/2;y??=canvas.clientHeight/2;
 state.pan.x=x-(x-state.pan.x)*next/old;state.pan.y=y-(y-state.pan.y)*next/old;state.zoom=next;
 applyViewport();rememberViewport();
}
function fitCanvas(markDirty=true) {
 const canvas=$('#canvas'),nodes=$$('.node');if(!canvas?.clientWidth||!nodes.length)return;
 const x=Math.min(...nodes.map(n=>n.offsetLeft)),y=Math.min(...nodes.map(n=>n.offsetTop));
 const right=Math.max(...nodes.map(n=>n.offsetLeft+n.offsetWidth)),bottom=Math.max(...nodes.map(n=>n.offsetTop+n.offsetHeight));
 state.zoom=clamp(Math.min((canvas.clientWidth-48)/(right-x),(canvas.clientHeight-48)/(bottom-y),1),.25,1.6);
 state.pan={x:(canvas.clientWidth-(right-x)*state.zoom)/2-x*state.zoom,y:(canvas.clientHeight-(bottom-y)*state.zoom)/2-y*state.zoom};
 applyViewport();rememberViewport(markDirty);
}
function installCanvasNavigation() {
 const canvas=$('#canvas');let pan;
 canvas.addEventListener('pointerdown',e=>{
  const background=!e.target.closest('.node,[data-edge],button');
  if(!(e.button===1||e.button===2||state.spacePan||(e.button===0&&background)))return;
  e.preventDefault();e.stopPropagation();
  pan={id:e.pointerId,x:e.clientX,y:e.clientY,origin:{...state.pan},moved:false};
  canvas.setPointerCapture?.(e.pointerId);canvas.classList.add('panning');
 },true);
 canvas.addEventListener('pointermove',e=>{
  if(!pan||e.pointerId!==pan.id)return;
  const dx=e.clientX-pan.x,dy=e.clientY-pan.y;pan.moved||=Math.abs(dx)+Math.abs(dy)>2;
  state.pan={x:pan.origin.x+dx,y:pan.origin.y+dy};applyViewport();
 });
 function end(){if(pan?.moved)rememberViewport();pan=null;canvas.classList.remove('panning');}
 canvas.addEventListener('pointerup',end);canvas.addEventListener('pointercancel',end);canvas.addEventListener('lostpointercapture',end);
 canvas.addEventListener('contextmenu',e=>e.preventDefault());
 canvas.addEventListener('wheel',e=>{
  e.preventDefault();
  if(e.ctrlKey){const r=canvas.getBoundingClientRect();zoomCanvas(state.zoom*Math.exp(-e.deltaY*.002),e.clientX-r.left,e.clientY-r.top);}
  else {state.pan.x-=e.shiftKey?e.deltaY:e.deltaX;state.pan.y-=e.shiftKey?0:e.deltaY;applyViewport();rememberViewport();}
 },{passive:false});
 if(!state.config.control_center.layout?.[pipe().id])requestAnimationFrame(()=>fitCanvas(false));
}

const controlHelp={
 saveConfig:'Записать черновик в config.json. Сначала проверяются граф, лимиты и подключения. Невалидная конфигурация не заменяет рабочую.',
 closeStudio:'Закрыть Studio и выгрузить WebView2 из памяти. Оверлей, хоткеи и текущий пайплайн продолжат работать; ⚙, хоткей или tray создадут Studio заново.',
 newPipeline:'Создать независимый маршрут для оверлея. Клик по пайплайну слева сохраняет и назначает его активным.',
 pipelineMenu:'Дублировать или удалить этот пайплайн.',
 addNode:'Добавить отдельный этап модели или проверки ответа.',addEdge:'Соединить этапы. Обычный переход выполняется после ответа; резервный можно привязать к ошибке.',
 canvas:'Тяните пустое поле левой кнопкой. Средняя кнопка или Space + перетаскивание двигают поле с любого места. Ctrl + колесо меняет масштаб.',
 arrange:'Разложить узлы по порядку основного маршрута.',fitGraph:'Уместить все узлы в видимой части поля.',
 zoomIn:'Увеличить масштаб графа.',zoomOut:'Уменьшить масштаб графа.',
 capturePreview:'Диагностика захвата без AI-запроса. Открытое Studio временно скрывается, чтобы не попасть в снимок. Уже скрытое или свёрнутое окно не восстанавливается.',
 settingsCapture:'Сделать диагностический снимок без запроса к модели. Уже скрытое или свёрнутое Studio не открывается.',
 sourceMode:'Мой список использует только выбранные модели. Каталог перебирает настроенные подключения. Список → каталог оставляет выбранные модели первыми, затем использует остальные.',
 sourceFree:'FREE определяется нулевой ценой в метаданных. Это не гарантия доступа без регистрации. При снятии фильтра в очередь попадают и платные модели.',
 sourceModality:'Для снимка выбираются IMAGE-модели. TEXT ONLY создаёт видимый этап зрения; его текст передаётся через {{vision.text}}.',
 sourceFloor:'Модели с ручной оценкой ниже этого порога исключаются из автоматического выполнения. У каталожных моделей по умолчанию оценка не задана (0).',
 sourceQuality:'Ваша общая оценка для каталожных моделей без индивидуальной настройки. Это не результат измерения качества.',
 poolPolicy:'Последовательно: следующая модель после ошибки. Одновременно: несколько запросов сразу. Резерв: новый запрос через интервал, пока ответа нет. Побеждает первый успешный ответ, прошедший порог допуска.',
 dispatchCalls:'Максимум реальных вызовов на этап, включая смену API-ключа и повторные попытки. Общий бюджет пайплайна может остановить этап раньше.',
 dispatchParallel:'Сколько запросов этого этапа могут выполняться одновременно. Также действует общий лимит пайплайна.',
 dispatchHedge:'Интервал запуска очередной модели, пока предыдущие ещё работают. После ошибки свободное место может быть занято раньше.',
 bWall:'Предельное время всего маршрута, включая этап зрения, ожидания и повторы.',bCalls:'Общее число реальных модельных запросов во всём пайплайне. Каждый повтор и новый ключ считаются отдельно.',
 bParallel:'Общий предел одновременно выполняющихся модельных запросов.',bProviders:'Сколько разных настроенных подключений может использовать один запуск.',
 bKeys:'Максимум разных ключей одного провайдера за весь запуск.',bConnect:'Срок установления соединения для native HTTP-адаптеров.',
 bFirst:'Срок до первого текста для потоковых адаптеров. OpenCode здесь возвращает полный ответ; для него действует таймаут всего ответа.',bResponse:'Предельная длительность отдельного ответа. Настройка модели, если задана, имеет приоритет.',bIdle:'Максимальная пауза между частями потокового ответа.',
 enableVision:'Отдельный VLM-этап читает снимок, затем передаёт текст основной модели. В текстовую модель изображение не отправляется.',
 keyPolicy:'Чередование, последний рабочий, последовательный или случайный здоровый ключ. Ошибка 401 исключает ключ, 429 временно ставит его на паузу.',
 keyMax:'Сколько разных ключей можно попробовать для одной модели. Действуют также общие лимиты ключей и вызовов.',keyCooldown:'На сколько секунд исключить ключ после rate limit (429). Таймаут не делает ключ навсегда неисправным.',
 providerDone:'Сохранить только это подключение, не применяя незаконченный черновик графа. Это не выполняет платный тест модели.',
 previewPlan:'Показать очередь и фильтры текущего этапа. Просмотр плана не вызывает AI.',
 exportConfig:'Экспорт содержит полные API-ключи. Храните JSON как секретный файл.',
 stopRun:'Отменить текущий запрос и его конкурирующие попытки. Уже принятый сервером запрос может расходовать квоту.',
 ghost:'Пропускать мышь сквозь оверлей. Для перетаскивания, resize и кнопки настроек сначала выключите Ghost Mode.',
 stealth:'Запросить исключение окна из поддерживаемого Windows capture. Не гарантирует невидимость во всех источниках OBS.',
};
const modelFieldHelp={priority:'Большее число запускается раньше в приоритетном порядке.',quality_score:'Ваша оценка качества. Модель ниже порога пула не участвует автоматически.',timeout_ms:'Таймаут полного ответа этой модели; имеет приоритет над общим значением.',first_token_timeout_ms:'Время до первого текста. Для buffered OpenCode не измеряется.',max_attempts:'Число повторных запросов с одним ключом; каждый расходует общий бюджет.',weight:'Вес вероятности при взвешенном выборе.'};
function installContextHelp() {
 let held=false,sticky=false,hover=null,x=0,y=0;
 const tip=document.createElement('div');tip.id='contextHelp';tip.className='context-help hidden';tip.setAttribute('role','tooltip');document.body.append(tip);
 function hide(){tip.classList.add('hidden');}
 function textFor(el) {
  const custom=el.closest('[data-help]');if(custom)return custom.dataset.help;
  const target=el.closest('button,input,select,textarea,summary,[data-node],.metric-chip');
  if(!target)return '';
  if(controlHelp[target.id])return controlHelp[target.id];
  if(target.dataset.mfield)return modelFieldHelp[target.dataset.mfield.split(',')[1]]||'Параметр этой модельной попытки.';
  if(target.dataset.pipeline)return 'Сохранить изменения и назначить этот пайплайн активным для оверлея. Новый запрос использует его сразу; отдельной кнопки запуска нет.';
  if(target.dataset.port)return target.dataset.port==='in'?'Вход этапа.':'Потяните к другому этапу. После ответа запрос пойдёт по связи; ошибки настраиваются отдельно.';
  if(target.dataset.node)return target.classList.contains('screen')?'Изображение из общего RAM-only capture path.':target.classList.contains('output')?'Финальный ответ отправляется в оверлей.':'Выберите этап для настройки. Тяните карточку, чтобы переместить её.';
  return target.title||target.getAttribute('aria-label')||target.labels?.[0]?.textContent||target.textContent.trim().slice(0,180);
 }
 function show(){
  if(!(held||sticky)||!hover){hide();return;}
  const text=textFor(hover);if(!text){hide();return;}
  const parent=$('#dialog').open?$('#dialog'):document.body;if(tip.parentNode!==parent)parent.append(tip);
  tip.textContent=text;tip.classList.remove('hidden');
  tip.style.left=Math.max(8,Math.min(x+16,window.innerWidth-316))+'px';
  tip.style.top=Math.max(8,Math.min(y+18,window.innerHeight-tip.offsetHeight-10))+'px';
 }
 document.addEventListener('pointermove',e=>{hover=e.target;x=e.clientX;y=e.clientY;show();});
 document.addEventListener('keydown',e=>{
  const editable=e.target.closest?.('input,textarea,select,[contenteditable="true"]');
  if(e.code==='Space'&&!editable&&!e.target.closest?.('button')){state.spacePan=true;e.preventDefault();}
  if(e.key==='Alt'&&!e.ctrlKey&&!e.getModifierState?.('AltGraph')){held=true;e.preventDefault();show();}
  if(e.key==='Escape'){sticky=false;held=false;hide();}
 });
 document.addEventListener('keyup',e=>{if(e.code==='Space')state.spacePan=false;if(e.key==='Alt'){held=false;show();}});
 window.addEventListener('blur',()=>{held=false;sticky=false;state.spacePan=false;hide();});
 document.addEventListener('pointerleave',hide);
 $('#dialog').addEventListener('close',hide);
 $('#helpToggle').onclick=()=>{sticky=!sticky;$('#helpToggle').classList.toggle('active',sticky);if(sticky)notify('Подсказки включены. Наведите указатель на элемент.');show();};
}

function renderAttemptState() {
 const root=$('#attemptState');if(!root)return;
 const job=state.job,events=job?.trace?.events||[];
 if(!job?.id||job.cleared||job.purpose==='connection_test'){root.innerHTML='';return;}
 const attempts=new Map();
 for(const e of events) {
  if(!e.attempt||!['attempt_start','first_token','attempt_end'].includes(e.kind))continue;
  const a=e.attempt,key=a.id||[e.node_id,a.provider_id,a.model_id,a.credential_id].join('/');
  const old=attempts.get(key)||{};attempts.set(key,{...old,...e,start:old.start??e.at_ms});
 }
 const active=[...attempts.values()].filter(e=>e.kind!=='attempt_end');
 const cfg=state.config.pipelines.find(p=>p.id===job?.trace?.pipeline_id)||pipe(),budget=job?.trace?.budget||cfg?.budget;
 root.innerHTML=`<button class="run-summary-button" id="openRunTrace" data-help="Фактические запросы: каждое событие attempt_start учитывает один модельный вызов. Откройте полный журнал, чтобы увидеть ключи, ошибки и отмены."><span>${job?.running?'Выполняются: '+active.length:'Последний запуск'}</span><span>Вызовы ${attempts.size} / ${budget?.max_model_calls??'—'}</span><span>Журнал →</span></button>${job?.running?active.map(e=>`<div class="live-attempt"><span class="live-dot"></span><span>${esc(shortModel(e.attempt.model_id))}<small>${esc(e.attempt.transport||'')} · ${esc(provider(e.attempt.provider_id)?.label||e.attempt.provider_id)} · ${esc(e.attempt.credential_alias||e.attempt.credential_id||'без ключа')}</small></span><time>+${(e.start/1000).toFixed(2)} с</time><b>${e.kind==='first_token'?'Текст':'Ожидание'}</b></div>`).join(''):''}`;
 $('#openRunTrace').onclick=()=>{state.page='runs';renderPage();};
}
