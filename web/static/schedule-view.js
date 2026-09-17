'use strict';
const orderNames={priority:'По приоритету списка',round_robin:'Чередовать первую модель',weighted:'По весам',fastest_healthy:'По надёжности и скорости'};
const dispatchNames={sequential:'После ошибки',staggered_race:'С задержкой резерва',parallel_race:'Одновременно'};
function poolOrder(pool){return pool.order||(['round_robin','weighted','fastest_healthy'].includes(pool.policy)?pool.policy:'priority');}
function poolDispatch(pool){return pool.dispatch||(['parallel_race','staggered_race'].includes(pool.policy)?pool.policy:'sequential');}
function setPoolSchedule(pool,order,dispatch){
 pool.order=order;pool.dispatch=dispatch;
 // Legacy field is kept readable, but schema 3's independent fields are authoritative.
 pool.policy=dispatch==='sequential'?order:dispatch;
}
function scheduleCaption(pool){return dispatchNames[poolDispatch(pool)]||poolDispatch(pool);}
function scheduleFields(pool,prefix='pool'){
 const dispatch=poolDispatch(pool),racing=dispatch!=='sequential',numeric=prefix==='pool'?'dispatch':prefix;
 return `${selectField('Кого пробовать первым',prefix+'Order',orderNames,poolOrder(pool),'Приоритет и качество не меняются от скорости одного ответа. Чередование меняет только первую модель следующего запроса.')}${selectField('Когда запускать следующую',prefix+'Policy',dispatchNames,dispatch,'После ошибки — одна попытка за раз. Резерв через задержку запускается, пока нет принятого ответа. Одновременно — свободные места заполняются сразу.')}
 <div class="dispatch-fields"><div class="form-row">${field('Вызовов на этап',numeric+'Calls',pool.max_calls,'number')}${racing?field('Одновременно',numeric+'Parallel',pool.max_parallel,'number'):''}</div>${dispatch==='staggered_race'?field('Резерв через, мс',numeric+'Hedge',pool.hedge_delay_ms,'number','Первый запрос продолжает выполняться. После задержки запускается резерв, если ещё нет принятого ответа.'):''}</div>
 <div class="dispatch-effective" data-help="Меньшее из лимитов этапа и всего пайплайна. Повтор или другой ключ расходует ещё один вызов.">Фактически: до ${racing?Math.min(pool.max_parallel,pipe().budget.max_parallel_calls):1} одновременно · до ${Math.min(pool.max_calls,pipe().budget.max_model_calls)} вызовов</div>`;
}
function bindSchedule(pool,prefix,changed){
 const numeric=prefix==='pool'?'dispatch':prefix;
 $('#'+prefix+'Order').onchange=e=>{setPoolSchedule(pool,e.target.value,poolDispatch(pool));dirty();changed();};
 $('#'+prefix+'Policy').onchange=e=>{setPoolSchedule(pool,poolOrder(pool),e.target.value);dirty();changed();};
 for(const [suffix,key]of [['Calls','max_calls'],['Parallel','max_parallel'],['Hedge','hedge_delay_ms']])bindValue(numeric+suffix,pool,key,true,changed);
}
