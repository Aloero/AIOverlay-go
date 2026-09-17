'use strict';

function renderSettings(){
 const c=state.config,o=c.overlay,v=c.vpn||(c.vpn={enabled:false,engine:'sing-box',config:''});
 v.engine='sing-box';
 $('#page').innerHTML=`<div class="page-head"><div><p class="eyebrow">РАБОЧЕЕ ОКРУЖЕНИЕ</p><h1>Приложение</h1></div></div><div class="scroll-page"><div class="grid-2 settings-layout">
 <section class="card"><h3>Оверлей</h3><p class="card-sub">Размер меняйте за край или угол окна в режиме редактирования.</p><div class="field"><label for="opacity">Прозрачность <span id="opacityValue">${Math.round(o.opacity*100)}%</span></label><input id="opacity" type="range" min="15" max="100" value="${Math.round(o.opacity*100)}"></div>${field('Размер шрифта','fontSize',o.font_size,'number')}<label class="switch"><input type="checkbox" id="ghost" ${o.click_through?'checked':''}>Ghost Mode: клики проходят сквозь окно</label><label class="switch"><input type="checkbox" id="stealth" ${o.stealth?'checked':''}>Исключать из поддерживаемого захвата</label><div class="help">Исключение зависит от Windows и метода захвата OBS. Проверьте свою запись. Оно не гарантирует невидимость при любом способе захвата.</div><button class="button" id="settingsCapture">${icon('screen')}Проверить захват</button></section>
 <section class="card"><h3>Горячие клавиши</h3><p class="card-sub">Комбинация с Ctrl, Alt, Shift или Win. Изменения применяются после сохранения.</p><div class="settings-keys">${[['analyze','Анализ экрана'],['ghost','Ghost Mode'],['clear','Очистить / отменить'],['control','Открыть Studio']].map(([key,label])=>field(label,'hotkey_'+key,o.hotkeys[key]||'')).join('')}</div><label class="switch"><input type="checkbox" id="stationary" ${o.stationary_cursor_ms>0?'checked':''}>Анализ после 10 секунд неподвижного курсора</label><label class="switch"><input type="checkbox" id="autoOpen" ${c.control_center.auto_open?'checked':''}>Открывать встроенную Studio при запуске</label><div class="help">Закрытие Studio выгружает WebView2; оверлей продолжает работать. Чтобы вернуть окно, используйте горячую клавишу Studio.</div></section>
 <section class="card span-2 vpn-card"><div class="vpn-head"><div><h3>AI VPN</h3><p class="card-sub">Изолированный tunnel только для запросов к AI providers. Системный VPN, TUN и Windows proxy не включаются.</p></div><label class="switch"><input type="checkbox" id="vpnEnabled" ${v.enabled?'checked':''}>Включён</label></div><div class="vpn-scope"><span>ENGINE <b>sing-box 1.14.1</b></span><span>LISTEN <b>127.0.0.1 · случайный порт · auth</b></span><span>SCOPE <b>AI requests only</b></span></div><div class="field"><label for="vpnConfig">Конфигурация tunnel</label><textarea id="vpnConfig" class="code-editor vpn-editor" rows="12" spellcheck="false" placeholder='{"type":"vless","server":"vpn.example","server_port":443,"uuid":"…","tls":{"enabled":true}}'>${esc(vpnConfigText(v.config))}</textarea><small>Вставьте один sing-box outbound JSON (VLESS, Hysteria2, TUIC, Trojan, Shadowsocks, VMess и т. д.) или полный sing-box JSON ровно с одним tunnel outbound. DNS выбранного outbound сохраняется. Inbounds, TUN и системные маршруты не импортируются. Внешнее соединение использует IPv4.</small></div><div class="actions-row"><button class="button" id="vpnCheck">Проверить конфиг</button><span class="vpn-status" id="vpnStatus">${v.enabled?'Будет применён ко всем AI-запросам после сохранения.':'Выключен: AI-запросы идут обычным сетевым путём.'}</span></div><div class="help warning">VPN-конфиг может содержать UUID, пароли и ключи. Он хранится в общем переносимом config.json — обращайтесь с ним как с секретным файлом.</div></section>
 <section class="card span-2"><div class="section-line"><h3>Диагностика</h3><button class="button small" id="refreshDiagnostics">Обновить</button></div><div id="runtimeDiagnostics" class="diagnostics-line">Проверка состояния…</div><label class="switch"><input id="persistStats" type="checkbox" ${c.app.persist_runtime_stats?'checked':''}>Сохранять статистику моделей в config.json</label></section>
 </div></div>`;
 $('#opacity').oninput=e=>{o.opacity=Number(e.target.value)/100;$('#opacityValue').textContent=e.target.value+'%';dirty();};bindValue('fontSize',o,'font_size',true);
 for(const [id,key]of [['ghost','click_through'],['stealth','stealth']])$('#'+id).onchange=e=>{o[key]=e.target.checked;dirty();};
 for(const key of ['analyze','ghost','clear','control'])bindValue('hotkey_'+key,o.hotkeys,key);
 $('#stationary').onchange=e=>{o.stationary_cursor_ms=e.target.checked?10000:0;dirty();};$('#autoOpen').onchange=e=>{c.control_center.auto_open=e.target.checked;dirty();};
 $('#vpnEnabled').onchange=e=>{v.enabled=e.target.checked;dirty();$('#vpnStatus').textContent=e.target.checked?'Будет применён ко всем AI-запросам после сохранения.':'Выключен: AI-запросы идут обычным сетевым путём.';};
 $('#vpnConfig').oninput=e=>{v.config=vpnConfigValue(e.target.value);dirty();$('#vpnStatus').textContent='Есть непроверенные изменения.';};
 $('#vpnCheck').onclick=guarded(async()=>{const b=$('#vpnCheck'),status=$('#vpnStatus');b.disabled=true;status.textContent='Проверка…';try{const candidate={enabled:true,engine:'sing-box',config:vpnConfigValue($('#vpnConfig').value)};const result=await api('/api/vpn/check','POST',candidate);status.textContent=result.check==='engine'?'Конфиг принят sing-box; подключение к серверу не проверялось.':'Структура принята; для проверки движком нужен Windows runtime.';}catch(e){status.textContent='Ошибка: '+e.message;throw e;}finally{if(b.isConnected)b.disabled=false;}});
 $('#settingsCapture').onclick=guarded(previewCapture);
 $('#persistStats').onchange=e=>{c.app.persist_runtime_stats=e.target.checked;dirty();};
 $('#refreshDiagnostics').onclick=guarded(refreshDiagnostics);guarded(refreshDiagnostics)();
}

async function refreshDiagnostics(){
 const info=await api('/api/diagnostics'),root=$('#runtimeDiagnostics');if(!root)return;
 const lines=[];
 for(const name of ['overlay','studio']){
  const d=info[name],p=d?.protection;if(!d)continue;
  if(name==='studio')lines.push('Studio: '+(d.loaded?'открыта':'выгружена'));
  if(p)lines.push(name+': защита '+(!p.requested?'выключена':p.applied?'применена Windows ('+p.mode+')':'НЕ применена')+(p.error?' · '+p.error:''));
  if(p?.composition_error)lines.push('Дополнительное исключение: '+p.composition_error);
  if(d.hotkey_error)lines.push('Горячие клавиши: '+d.hotkey_error);
  if(d.ghost_transient)lines.push('Ghost переключён хоткеем для текущего сеанса; значение запуска хранится отдельно.');
 }
 if(info.vpn)lines.push('AI VPN: '+(!info.vpn.enabled?'выключен':info.vpn.running?'движок запущен; доступ к серверу проверяется запросом':info.vpn.starting?'запускается':'ожидает AI-запрос')+(info.vpn.error?' · '+info.vpn.error:''));
 if(info.catalog?.error)lines.push('Каталог: '+info.catalog.error);
 root.textContent=lines.join('\n')||'Нативная диагностика доступна в Windows-приложении.';
}
