'use strict';

function vpnOutbound(v){if(!v?.config||typeof v.config!=='object')return null;return v.config.type?v.config:(v.config.outbounds||[]).find(o=>!['direct','block','dns'].includes(o.type));}
function vpnCard(v){
 const out=vpnOutbound(v);
 return `<section class="card span-2 vpn-card"><div class="vpn-head"><div><h3>AI VPN</h3><p class="card-sub">Только запросы AIOverlay. Сеть остальных приложений не меняется.</p></div><label class="switch"><input type="checkbox" id="vpnEnabled" ${v.enabled?'checked':''}>Включён</label></div><div class="vpn-profile-line"><strong id="vpnProfileName">${esc(v.name||'Основной')}</strong><span id="vpnProtocol">${esc(out?.type?.toUpperCase()||'Профиль не указан')}</span><span id="vpnRuntimeState">${v.enabled?'Ожидает применения или первого AI-запроса':'Выключен'}</span></div><details id="vpnEditor"><summary>Изменить профиль</summary>${field('Название профиля','vpnName',v.name||'Основной')}<div class="field"><label for="vpnImportLink">Ссылка подключения или sing-box JSON</label><textarea id="vpnImportLink" rows="2" autocomplete="off" spellcheck="false" placeholder="vless://… или hysteria2://…"></textarea><small>Ссылки: VLESS, Hysteria2 / hy2, Trojan. Другие протоколы — JSON.</small></div><button class="button small" id="vpnImport">Разобрать и вставить</button><p id="vpnImportWarning" class="warning-text"></p><div class="field"><label for="vpnConfig">Профиль sing-box</label><textarea id="vpnConfig" class="code-editor vpn-editor" rows="9" spellcheck="false" autocomplete="off"></textarea></div><p class="secret-notice">UUID, пароли и ключи профиля хранятся в общем config.json. Это секретный файл.</p></details><div class="actions-row"><button class="button small" id="vpnCheck">Проверить конфиг</button><button class="button small" id="vpnProbe">Проверить соединение</button></div><div class="vpn-status" id="vpnStatus">${v.enabled?'При ошибке туннеля AI-запрос не отправляется напрямую.':'VPN не используется.'}</div><details class="vpn-details"><summary>Технические сведения</summary><p>sing-box 1.14.4 · authenticated 127.0.0.1 proxy · IPv4 endpoint. Без TUN, системного proxy и изменения маршрутов Windows. Проверка JSON не подтверждает доступность сервера.</p></details></section>`;
}
function bindVPN(v){
 const profile=()=>{const out=vpnOutbound(v);$('#vpnProfileName').textContent=v.name||'Основной';$('#vpnProtocol').textContent=out?.type?.toUpperCase()||'Профиль не указан';};
 $('#vpnEnabled').onchange=e=>{v.enabled=e.target.checked;dirty();$('#vpnStatus').textContent='Изменение применится после сохранения.';};
 $('#vpnEditor').ontoggle=e=>{if(e.target.open){$('#vpnConfig').value=vpnConfigText(v.config);}else{$('#vpnConfig').value='';$('#vpnImportLink').value='';}};
 $('#vpnName').onchange=e=>{v.name=e.target.value.trim();dirty();profile();};
 $('#vpnConfig').oninput=e=>{v.config=vpnConfigValue(e.target.value);dirty();profile();$('#vpnStatus').textContent='Профиль изменён, проверка ещё не выполнена.';};
 $('#vpnImport').onclick=guarded(async()=>{
  const b=$('#vpnImport');b.disabled=true;
  try{const result=await api('/api/vpn/import','POST',{text:$('#vpnImportLink').value});v.config=result.config;v.name=result.name;v.engine='sing-box';$('#vpnConfig').value=vpnConfigText(v.config);$('#vpnName').value=v.name;$('#vpnImportLink').value='';$('#vpnImportWarning').textContent=(result.warnings||[]).join(' ');profile();dirty();$('#vpnStatus').textContent='Импортировано в черновик. Проверьте конфиг перед применением.';}
  finally{if(b.isConnected)b.disabled=false;}
 });
 $('#vpnCheck').onclick=guarded(async()=>{
  const b=$('#vpnCheck'),status=$('#vpnStatus');b.disabled=true;status.textContent='Проверка параметров…';
  try{const r=await api('/api/vpn/check','POST',{enabled:true,engine:'sing-box',config:v.config});status.textContent=r.check==='engine'?'Параметры приняты движком. Сетевое соединение ещё не проверялось.':'Структура принята. Проверка движком доступна на Windows.';}
  catch(e){status.textContent='Параметры отклонены: '+e.message;throw e;}finally{if(b.isConnected)b.disabled=false;}
 });
 $('#vpnProbe').onclick=guarded(openVPNProbe);
}
async function openVPNProbe(){
 if(state.dirty)throw new Error('Сохраните и примените настройки перед сетевой проверкой. Проверяется сохранённый профиль.');
 if(!appliedConfig?.vpn?.enabled)throw new Error('Сначала включите и сохраните AI VPN.');
 if(state.job?.running)throw new Error('Сначала завершите текущий AI-запрос.');
 let target='https://example.com';for(const p of appliedConfig.providers){try{const u=new URL(p.base_url);if(u.protocol==='https:'){target=u.origin;break;}}catch{}}
 modal('Проверить соединение',`${field('HTTPS-адрес без пути','vpnProbeTarget',target,'url')}<p class="note">Один HTTPS HEAD через сохранённый VPN к этому адресу. Без API-ключа, prompt и screenshot. HTTP 401/403/404 означает, что сервер ответил, а не успешную AI-авторизацию. При ошибке прямого запроса не будет.</p><label class="switch"><input id="vpnProbeConsent" type="checkbox">Разрешаю сетевую проверку этого адреса</label><div class="dialog-footer"><button class="button primary" id="vpnProbeNow" disabled>Проверить через VPN</button></div><div id="vpnProbeResult" aria-live="polite"></div>`,'СЕТЕВАЯ ПРОВЕРКА · НЕ AI');
 $('#vpnProbeConsent').onchange=e=>$('#vpnProbeNow').disabled=!e.target.checked;
 $('#vpnProbeNow').onclick=guarded(async()=>{
  const b=$('#vpnProbeNow'),root=$('#vpnProbeResult');b.disabled=true;root.textContent='Устанавливаю соединение…';
  try{const r=await api('/api/vpn/probe','POST',{target:$('#vpnProbeTarget').value.trim(),confirm:$('#vpnProbeConsent').checked});if(root.isConnected)root.textContent=`Получен HTTP ${r.http_status} за ${(r.duration_ms/1000).toFixed(2)} с. Проверено ${new Date(r.checked_at).toLocaleTimeString()}. Доступ модели и баланс не проверялись.`;}
  catch(e){if(root.isConnected)root.textContent='Соединение не подтверждено. '+e.message;throw e;}finally{if(b.isConnected)b.disabled=false;}
 });
}
