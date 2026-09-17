'use strict';

// A dialog draft is independent of the main graph config until it is committed.
let dialogDraft = null, closePromise = null, choicePromise = null;
function normalizeConfig(config) {
 const c=ensureRuntimeDefaults(config);
 c.providers||=[];c.pipelines||=[];c.control_center.layout||={};
 for(const p of c.providers){p.credential_pool||={};p.credential_pool.keys||=[];p.request_defaults||={};p.headers||={};p.credential_env||=[];}
 function pool(p){if(!p)return;p.models||=[];if(p.selection)p.selection.provider_ids||=[];pool(p.vision_preprocessor);}
 for(const p of c.pipelines){p.nodes||=[];p.edges||=[];for(const n of p.nodes)pool(n.model_pool);for(const e of p.edges)e.on||=[];}
 return c;
}
function unsavedChoice() {
 if(choicePromise)return choicePromise;
 const dialog=document.createElement('dialog');dialog.id='unsavedChoice';dialog.className='unsaved-dialog';
 dialog.innerHTML='<div class="dialog-head"><h2>Несохранённое подключение</h2></div><div class="choice-body">Сохранить изменения перед закрытием?<div class="dialog-footer"><button class="button" data-choice="cancel">Отмена</button><button class="button" data-choice="discard">Не сохранять</button><button class="button primary" data-choice="save">Сохранить</button></div></div>';
 document.body.append(dialog);
 choicePromise=new Promise(resolve=>{
  function finish(value){dialog.close();dialog.remove();choicePromise=null;resolve(value);}
  $$('[data-choice]',dialog).forEach(b=>b.onclick=()=>finish(b.dataset.choice));
  dialog.addEventListener('cancel',e=>{e.preventDefault();finish('cancel');});
  dialog.showModal();$('[data-choice="cancel"]',dialog).focus();
 });
 return choicePromise;
}
async function settleDialog() {
 const draft=dialogDraft;if(!draft)return true;
 if(draft.saving)await draft.saving;
 if(!draft.isDirty())return true;
 const choice=await unsavedChoice();
 if(choice==='cancel')return false;
 if(choice==='save'){await draft.save();if(draft.isDirty())return false;}
 else {draft.discard();dialogDraft=null;}
 return true;
}
async function closeModal(force=false) {
 if(force!==true && !await settleDialog())return false;
 dialogDraft=null;$('#dialog').close();return true;
}
async function closeStudio() {
 if(!native()){notify('Закройте вкладку браузера. Приложение работает отдельно в режиме разработки.');return false;}
 if(closePromise)return closePromise;
 closePromise=(async()=>{
  const active=document.activeElement;if(active?.matches('input,textarea,select'))active.dispatchEvent(new Event('change',{bubbles:true}));
  if(!await settleDialog())return false;
  if(savePromise)await savePromise;
  if(state.dirty)await saveConfig();
  // An edit made while PUT was in flight is still a draft. Never close over it.
  if(state.dirty||dialogDraft?.isDirty()){notify('Появились новые изменения. Studio остаётся открытой.');return false;}
  stopHotkeyEditing();
  await api('/desktop/close','POST');return true;
 })();
 try{return await closePromise;}finally{closePromise=null;}
}
window.__studioNativeClose=()=>guarded(closeStudio)();

let hotkeyLease=null, hotkeyEditing=false;
function hotkeyEditorActive(){return document.activeElement?.id?.startsWith('hotkey_')===true;}
function stopHotkeyEditing(){
 clearInterval(hotkeyLease);hotkeyLease=null;
 if(hotkeyEditing){hotkeyEditing=false;api('/api/hotkeys/editor','POST',{active:false}).catch(failure);}
}
function installHotkeyEditing(){
 document.addEventListener('focusin',()=>{
  if(!hotkeyEditorActive()||hotkeyEditing)return;
  hotkeyEditing=true;
  api('/api/hotkeys/editor','POST',{active:true}).catch(failure);
  hotkeyLease=setInterval(()=>{if(!hotkeyEditorActive()){stopHotkeyEditing();return;}api('/api/hotkeys/editor','POST',{active:true}).catch(failure);},5000);
 });
 document.addEventListener('focusout',()=>queueMicrotask(()=>{if(!hotkeyEditorActive())stopHotkeyEditing();}));
 window.addEventListener('blur',stopHotkeyEditing);
 document.addEventListener('visibilitychange',()=>{if(document.hidden)stopHotkeyEditing();});
 $('#dialog').addEventListener('cancel',e=>{e.preventDefault();guarded(closeModal)();});
}
