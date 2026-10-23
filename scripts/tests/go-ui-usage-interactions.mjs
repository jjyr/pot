async page => {
  const ready=async()=>{await page.waitForFunction(()=>document.querySelector('.page-content')?.getAttribute('aria-busy')==='false'&&!!document.querySelector('.usage-trend'));await page.evaluate(()=>window.usageFixture.verify());};
  const selectedRange=()=>page.locator('.usage-range').evaluate(node=>({start:node.dataset.start,end:node.dataset.end}));
  await page.waitForFunction(()=>/^(PASS|FAIL)/.test(document.querySelector('#result').textContent));
  if(!(await page.locator('#result').textContent()).startsWith('PASS'))throw Error(await page.locator('#result').textContent());
  await ready();
  if(await page.locator('.usage-range select,.usage-range input').count()||await page.locator('.usage-period-link').count()!==4)throw Error('Exactly four directly clickable timescales, without extra date controls');
  const last=page.locator('.usage-trend .chart-column').last();
  await last.focus();
  if(!await last.locator('.usage-tooltip').evaluate(node=>node.matches(':popover-open')))throw Error('Keyboard focus must show usage details');
  await page.keyboard.press('Escape');
  if(await last.locator('.usage-tooltip').evaluate(node=>node.matches(':popover-open')))throw Error('Escape must dismiss details');
  await last.click();
  if(!await last.locator('.usage-tooltip').evaluate(node=>node.matches(':popover-open')))throw Error('Tap must show usage details');
  await page.keyboard.press('Escape');
  const scenario=await page.locator('#fixture-case').inputValue();
  if(scenario==='trend'){
    for(const preset of ['fortnight','month','quarter','year']){
      const link=page.locator(`.usage-period-link[data-preset="${preset}"]`);
      await link.click();await ready();const current=await selectedRange();
      if(await page.locator('.usage-period-link[aria-pressed="true"]').count()!==1||await link.getAttribute('aria-pressed')!=='true')throw Error('Selected timescale must be marked');
      if(!await page.locator('.usage-range-toolbar>.control').last().isDisabled())throw Error('Next period must be disabled beyond the current natural period');
      if(preset==='fortnight'&&await page.locator('.chart-column').count()!==14)throw Error('Two weeks must retain fourteen dates');
      if(preset==='month'&&!current.start.endsWith('-01'))throw Error('Month must start on its first day');
      if(preset==='quarter'&&!/-0[147]-01$|-10-01$/.test(current.start))throw Error('Three months must align to a natural quarter');
      if(preset==='year'&&await page.locator('.chart-column').count()!==12)throw Error('Year must show twelve natural months');
      await page.locator('.usage-range-toolbar>.control').first().click();await ready();const previous=await selectedRange();
      if((new Date(current.start)-new Date(previous.end))/86400000!==1)throw Error('Adjacent periods must not overlap or skip a day');
      if(await link.getAttribute('aria-pressed')!=='true')throw Error('Timescale stays selected while browsing previous periods');
      await page.locator('.usage-range-toolbar>.control').last().click();await ready();
      if(JSON.stringify(await selectedRange())!==JSON.stringify(current))throw Error('Next period failed to return to current dates');
    }
    const year=Number(await page.locator('.usage-calendar-controls>span').textContent());
    for(let i=year;i>2024;i--){await page.locator('.usage-calendar-controls button').first().click();await ready();}
    if(await page.locator('.usage-day').count()!==366)throw Error('Calendar year must include leap day');
    await page.locator('.usage-day[data-date="2024-02-29"]').click();await ready();
    if(await page.locator('.chart-column').count()!==1||(await selectedRange()).start!=='2024-02-29')throw Error('Selecting calendar date must update all statistics');
    const request=await page.evaluate(()=>window.usageFixture.requests.find(row=>row.start==='2024-02-29'&&row.end==='2024-02-29'));
    if(request.scope!=='main'||request.owner!=='main')throw Error('Date selection lost conversation owner');
    await page.evaluate(()=>window.usageFixture.hold());
    await page.locator('.usage-period-link[data-preset="fortnight"]').click();
    await page.waitForFunction(()=>document.querySelector('.page-content')?.getAttribute('aria-busy')==='true');
    await page.locator('.usage-period-link[data-preset="month"]').click();await ready();
    const selected=await selectedRange();
    await page.evaluate(()=>window.usageFixture.release());
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));await ready();
    if(JSON.stringify(await selectedRange())!==JSON.stringify(selected))throw Error('Stale response replaced selected dates');
    await page.evaluate(()=>window.usageFixture.failCalendar(true));
    await page.locator('.usage-calendar-controls button').first().click();await ready();
    if(!await page.locator('.usage-calendar-error').count())throw Error('Annual failure must leave selected statistics available');
    await page.evaluate(()=>window.usageFixture.failCalendar(false));
    await page.locator('.usage-calendar-error button').click();await ready();
    if(!await page.locator('.usage-calendar').count())throw Error('Calendar retry failed');
  }
  const language=await page.locator('#fixture-locale').inputValue(),selected=await selectedRange();
  await page.locator('#fixture-locale').selectOption(language==='en'?'zh-CN':'en');
  await page.waitForFunction(expected=>document.querySelector('.usage-trend h2')?.textContent===expected,language==='en'?'用量趋势':'Usage trend');await ready();
  if(JSON.stringify(await selectedRange())!==JSON.stringify(selected))throw Error('Language switch lost date selection');
  return 'Four timescales, natural period navigation, distributions, calendar selection, stale requests and locale passed';
}
