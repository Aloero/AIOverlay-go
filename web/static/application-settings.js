'use strict';

function renderSettings(){
 const c=state.config,o=c.overlay,v=c.vpn||(c.vpn={enabled:false,engine:'sing-box',config:''});
 v.engine='sing-box';
 $('#page').innerHTML=`<div class="page-head"><div><h1>Приложение</h1></div></div><div class="scroll-page"><div class="grid-2 settings-layout">
 <section class="card"><h3>Оверлей</h3><p class="card-sub">Размер меняйте за край или угол окна в режиме редактирования.</p><div class="field"><label for="opacity">Прозрачность <span id="opacityValue">${Math.round(o.opacity*100)}%</span></label><input id="opacity" type="range" min="15" max="100" value="${Math.round(o.opacity*100)}"></div>${field('Размер шрифта','fontSize',o.font_size,'number')}<label class="switch"><input type="checkbox" id="ghost" ${o.click_through?'checked':''}>Ghost Mode: клики проходят сквозь окно</label><label class="switch"><input type="checkbox" id="stealth" ${o.stealth?'checked':''}>Исключать из поддерживаемого захвата</label><div class="help">Исключение зависит от Windows и метода захвата OBS. Проверьте свою запись. Оно не гарантирует невидимость при любом способе захвата.</div><button class="button" id="settingsCapture">${icon('screen')}Проверить захват</button></section>
 <section class="card"><h3>Горячие клавиши</h3><p class="card-sub">Комбинация с Ctrl, Alt, Shift или Win. Изменения применяются после сохранения.</p><div class="settings-keys">${[['analyze','Снимок и ответ'],['visibility','Показать / скрыть оверлей'],['ghost','Ghost Mode'],['clear','Очистить / отменить'],['control','Открыть Studio'],['screen_context','Снимок только в контекст'],['screen_decide','Снимок: пусть модель решит'],['session_pause','Пауза / продолжить фон'],['quit','Выход из приложения']].map(([key,label])=>field(label,'hotkey_'+key,o.hotkeys[key]||'')).join('')}</div><label class="switch"><input type="checkbox" id="stationary" ${o.stationary_cursor_ms>0?'checked':''}>Анализ после 10 секунд неподвижного курсора</label><label class="switch"><input type="checkbox" id="autoOpen" ${c.control_center.auto_open?'checked':''}>Открывать встроенную Studio при запуске</label><div class="help">Visibility скрывает только окно оверлея: текущий запрос и фон продолжаются, новый ответ не показывает окно обратно. Clear и Ghost — отдельные действия. Дополнительные screenshot-команды работают, только если маршрут настроен в активном пайплайне.</div></section>
 ${vpnCard(v)}
 <section class="card span-2"><div class="section-line"><h3>Диагностика</h3><button class="button small" id="refreshDiagnostics">Обновить</button></div><div id="runtimeDiagnostics" class="diagnostics-line">Проверка состояния…</div><label class="switch"><input id="persistStats" type="checkbox" ${c.app.persist_runtime_stats?'checked':''}>Сохранять статистику моделей в config.json</label></section>
 </div></div>`;
 $('#opacity').oninput=e=>{o.opacity=Number(e.target.value)/100;$('#opacityValue').textContent=e.target.value+'%';dirty();};bindValue('fontSize',o,'font_size',true);
 for(const [id,key]of [['ghost','click_through'],['stealth','stealth']])$('#'+id).onchange=e=>{o[key]=e.target.checked;dirty();};
 for(const key of ['analyze','visibility','ghost','clear','control','screen_context','screen_decide','session_pause','quit'])bindValue('hotkey_'+key,o.hotkeys,key);
 $('#stationary').onchange=e=>{o.stationary_cursor_ms=e.target.checked?10000:0;dirty();};$('#autoOpen').onchange=e=>{c.control_center.auto_open=e.target.checked;dirty();};
 bindVPN(v);
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
  if(d.user_hidden)lines.push('Оверлей: скрыт пользователем; ответы могут обновлять содержимое, но окно не появится само.');
 }
 if(info.vpn&&$('#vpnRuntimeState'))$('#vpnRuntimeState').textContent=!info.vpn.enabled?'Выключен':info.vpn.error?'AI-запросы заблокированы: ошибка VPN':info.vpn.running?'Движок запущен':info.vpn.starting?'Запускается':'Ожидает первого AI-запроса';
 if(info.vpn)lines.push('AI VPN: '+(!info.vpn.enabled?'выключен':info.vpn.running?'движок запущен; доступ к серверу проверяется запросом':info.vpn.starting?'запускается':'ожидает AI-запрос')+(info.vpn.error?' · '+info.vpn.error:''));
 if(info.session){const rows=info.session.sources||[];lines.push('Фон: '+(info.session.paused?'пауза':rows.length?rows.map(s=>s.name+': '+(s.running?'активен':'остановлен')).join(' · '):'источники не запущены'));}
 if(info.catalog?.error)lines.push('Каталог: '+info.catalog.error);
 root.textContent=lines.join('\n')||'Нативная диагностика доступна в Windows-приложении.';
}
