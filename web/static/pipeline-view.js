'use strict';

function renderPipeline() {
 const p=pipe();if(!p)return;
 if(!p.nodes.some(n=>n.id===state.node))state.node=p.nodes.find(n=>n.type==='model_pool')?.id;
 restoreViewport();
 $('#page').innerHTML=`<div class="page-head pipeline-head"><div class="pipeline-detail"><div class="active-indicator">${p.id===state.config.active_pipeline_id?'<i></i>АКТИВЕН':'ЧЕРНОВИК'}</div><input class="name-title" id="pipelineName" aria-label="Название пайплайна" value="${esc(p.name)}"></div><div class="head-actions"><button class="button subtle" id="pipelineMenu" aria-label="Параметры пайплайна">···</button><button class="button" id="previewPlan">План попыток</button><button class="button danger hidden" id="stopRun">Отменить запрос</button></div></div>
 <div class="workbench"><div class="editor-pane"><div class="budget-strip" id="budgetStrip"></div><div class="canvas-toolbar"><div class="tool-group"><button class="button small" id="addNode">+ Этап</button><button class="button small subtle" id="addEdge">${icon('link')}Связь</button></div><div class="tool-group"><button class="icon-button" id="zoomOut" aria-label="Уменьшить масштаб">−</button><small id="zoomLabel"></small><button class="icon-button" id="zoomIn" aria-label="Увеличить масштаб">+</button><button class="button small subtle" id="fitGraph">Уместить</button><button class="button small subtle" id="arrange">Упорядочить</button></div></div><div class="canvas" id="canvas" tabindex="0"><div class="canvas-inner" id="canvasInner"><svg class="graph-svg" id="graphLinks"></svg><div id="graphNodes"></div></div></div><div id="attemptState" class="attempt-state"></div><div class="output-box"><div class="output-head"><strong>Ответ <span id="runState" class="run-state"></span></strong><span id="outputMeta" class="output-meta"></span></div><div id="outputText" class="output-text"></div></div></div><aside class="inspector" id="inspector"></aside></div>`;
 $('#pipelineName').onchange=e=>{p.name=e.target.value.trim()||p.id;dirty();renderPipelineList();};
 $('#pipelineMenu').onclick=pipelineMenu;
 $('#previewPlan').onclick=guarded(()=>{const pool=selectedNode()?.model_pool||p.nodes.find(n=>n.model_pool)?.model_pool;if(!pool)throw new Error('Добавьте этап моделей.');return showPoolPlan(pool,!pool.vision_preprocessor);});
 $('#stopRun').onclick=guarded(stopRun);
 $('#addNode').onclick=addNodeDialog;$('#addEdge').onclick=()=>{state.edge='__new';renderInspector();};
 $('#arrange').onclick=()=>{arrange();dirty();renderGraph();requestAnimationFrame(()=>fitCanvas());};
 $('#fitGraph').onclick=()=>fitCanvas();$('#zoomIn').onclick=()=>zoomCanvas(state.zoom+.1);$('#zoomOut').onclick=()=>zoomCanvas(state.zoom-.1);
 renderBudget();renderGraph();renderInspector();installCanvasNavigation();updateOutput();
}

const outcomeNames={success:'После ответа',any_error:'При любой ошибке',auth_failed:'Ошибка ключа',rate_limited:'Лимит частоты',quota_exceeded:'Квота исчерпана',timeout:'Таймаут',first_token_timeout:'Нет первого текста',stream_idle_timeout:'Пауза в стриме',network_error:'Ошибка сети',model_unavailable:'Модель недоступна',region_restricted:'Ограничение региона',provider_unavailable:'Провайдер недоступен',unsupported_modality:'Неподдерживаемый вход',invalid_response:'Некорректный ответ',validation_failed:'Проверка не пройдена',cancelled:'Отмена'};
function outcomeLabel(values){return values.map(v=>outcomeNames[v]||v).join(', ');}
function renderEdgeInspector(root) {
 let edge=pipe().edges.find(e=>e.id===state.edge),fresh=!edge;
 if(!edge)edge={from:state.node||pipe().entry_node_id,to:pipe().output_node_id,on:['success'],priority:10,delay_ms:0,max_visits:1};
 const nodes=Object.fromEntries(pipe().nodes.map(n=>[n.id,n.name]));
 const simple=edge.on.length===1&&['success','any_error'].includes(edge.on[0]);
 root.innerHTML=`<div class="inspector-head"><h3>Связь этапов</h3><button class="icon-button" id="backNode" aria-label="К настройкам этапа">←</button></div><div class="inspector-body">${selectField('Откуда','edgeFrom',nodes,edge.from)}${selectField('Куда','edgeTo',nodes,edge.to)}${selectField('Когда переходить','edgeMode',{success:'После успешного ответа',any_error:'При любой ошибке',custom:'Указать ошибки…'},simple?edge.on[0]:'custom')}<div id="edgeOutcomes" class="outcome-grid ${simple?'hidden':''}">${outcomes.filter(v=>!['success','any_error'].includes(v)).map(v=>`<label><input type="checkbox" data-outcome="${v}" ${edge.on.includes(v)?'checked':''}>${esc(outcomeNames[v])}</label>`).join('')}</div><details><summary>Дополнительно</summary>${field('Приоритет связи','edgePriority',edge.priority,'number')}${field('Максимум переходов за запуск','edgeVisits',edge.max_visits,'number')}${field('Пауза перед переходом, мс','edgeDelay',edge.delay_ms||0,'number')}</details><div class="actions-row"><button class="button primary" id="applyEdge">Применить</button>${fresh?'':'<button class="button danger" id="deleteEdge">Удалить связь</button>'}</div></div>`;
 $('#edgeMode').onchange=e=>$('#edgeOutcomes').classList.toggle('hidden',e.target.value!=='custom');
 $('#backNode').onclick=()=>{state.edge=null;state.tab='models';renderInspector();drawLinks();};
 $('#applyEdge').onclick=()=>{
  const mode=$('#edgeMode').value,on=mode==='custom'?$$('[data-outcome]:checked').map(e=>e.dataset.outcome):[mode];
  if(!on.length){notify('Выберите хотя бы одну ошибку.',true);return;}
  Object.assign(edge,{from:$('#edgeFrom').value,to:$('#edgeTo').value,on,priority:finite($('#edgePriority').value,10),max_visits:Math.max(1,finite($('#edgeVisits').value,1)),delay_ms:Math.max(0,finite($('#edgeDelay').value))});
  if(fresh){edge.id=uid('edge');pipe().edges.push(edge);}state.edge=edge.id;dirty();renderGraph();notify('Связь обновлена.');
 };
 if(!fresh)$('#deleteEdge').onclick=()=>{pipe().edges=pipe().edges.filter(e=>e.id!==edge.id);state.edge=null;dirty();renderGraph();renderInspector();};
}
