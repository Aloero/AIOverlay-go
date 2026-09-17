'use strict';

async function previewFinalPrompt(prompt,node,isVision=false){
 const pool=isVision?node?.model_pool?.vision_preprocessor:node?.model_pool;
 const slots=(pool?.models||[]).filter(m=>m.enabled),hasPreprocessor=!!node?.model_pool?.vision_preprocessor&&!isVision;
 const onlyText=(!pool.selection||pool.selection.mode==='manual')&&slots.length>0&&slots.every(m=>modelMeta(m)&&!modelMeta(m).input_modalities?.includes('image'));
 const imageNote=hasPreprocessor?'Изображение не прикладывается: этот этап получает {{vision.text}}.':pool?.selection?.modality==='text'||onlyText?'Изображение не прикладывается к текстовой модели.':'Изображение прикладывается только к совместимой модели при наличии screenshot во входе.';
 modal('Итоговый prompt',`<div class="prompt-provenance"><strong>${esc(node?.name||'Этап')} ${isVision?' / Извлечение':' / Решение'}</strong><p>${esc(imageNote)}</p><small id="promptDataSource">Синтетические значения. Данные предыдущих снимков не открываются автоматически.</small></div><div class="grid-3">${field('{{input.text}} · исходный ввод','previewInput','input text')}${field('{{previous.output}} · предыдущий этап','previewPrevious','previous output')}${field('{{vision.text}} · этап извлечения','previewVision','vision text')}${field('{{audio.text}} · текущая расшифровка','previewAudio','audio transcript')}${field('{{context.text}} · выбранный контекст','previewContext','conversation context')}</div><div class="actions-row"><button class="button" id="applyPreview">Обновить</button><button class="button subtle" id="loadLastPrompt">Подставить данные последнего запуска</button></div><div id="lastPromptInfo" role="status"></div><h3>System prompt</h3><pre class="answer-preview" id="previewSystem"></pre><h3>User prompt</h3><pre class="answer-preview" id="previewUser"></pre>`,'БЕЗ СНИМКА И AI-ЗАПРОСОВ');
 const systemElement=$('#previewSystem'),userElement=$('#previewUser');let renderSerial=0;
 const refresh=async()=>{const order=++renderSerial;const values={input_text:$('#previewInput').value,previous_output:$('#previewPrevious').value,vision_text:$('#previewVision').value,audio_text:$('#previewAudio').value,context_text:$('#previewContext').value};const rendered=await api('/api/prompts/preview','POST',{prompt,values});if(order!==renderSerial||!systemElement.isConnected)return;systemElement.textContent=rendered.system;userElement.textContent=rendered.user;};
 let serial=0;
 const lastButton=$('#loadLastPrompt'),info=$('#lastPromptInfo');
 lastButton.disabled=!node;
 lastButton.onclick=guarded(async()=>{
  const current=++serial;lastButton.disabled=true;
  try{
   const last=await api('/api/prompts/last');if(current!==serial||!info.isConnected)return;
   const id=node.id+(isVision?'/vision':'');
   const matching=last.pipeline_id===pipe().id?(last.items||[]).filter(p=>p.node_id===id):[];
   const row=matching.at(-1);
   if(!row){info.textContent='В последнем запуске нет данных этого этапа. Предпросмотр не изменён.';return;}
   $('#previewInput').value=row.values.input_text;$('#previewPrevious').value=row.values.previous_output;$('#previewVision').value=row.values.vision_text;$('#previewAudio').value=row.values.audio_text||'';$('#previewContext').value=row.values.context_text||'';
   $('#promptDataSource').textContent='Данные запуска '+last.run_id+' · '+row.node_id+' · '+row.provider_id+'/'+row.model_id;
   info.textContent=(row.image_attached?'В этой попытке изображение прикладывалось.':'Изображение не прикладывалось.')+' Используется текущий черновик инструкций с прежними текстовыми данными. Сам снимок не хранится в предпросмотре.'+(row.truncated?' Значения сокращены до лимита памяти; это не полный prompt.':'');
   await refresh();
  }finally{if(lastButton.isConnected)lastButton.disabled=false;}
 });
 $('#applyPreview').onclick=guarded(refresh);await refresh();
}
