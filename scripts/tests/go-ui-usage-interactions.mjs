async page => {
  const ready=async()=>{await page.waitForFunction(()=>document.querySelector('.page-content')?.getAttribute('aria-busy')==='false'&&!!document.querySelector('.usage-trend'));await page.evaluate(()=>window.usageFixture.verify());};
  await page.waitForFunction(()=>/^(PASS|FAIL)/.test(document.querySelector('#result').textContent));
  if(!(await page.locator('#result').textContent()).startsWith('PASS'))throw Error(await page.locator('#result').textContent());
  await ready();
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
    await page.locator('.usage-period-select').selectOption('week');await ready();
    if(await page.locator('.chart-column').count()!==7)throw Error('Natural week must retain seven dates');
    const current=await page.locator('.usage-range').getAttribute('data-start');
    await page.locator('.usage-range-toolbar>.control').first().click();await ready();
    if(await page.locator('.usage-range').getAttribute('data-start')===current)throw Error('Previous period failed');
    await page.locator('.usage-period-select').selectOption('year');await ready();
    if(await page.locator('.chart-column').count()!==12)throw Error('Full year must show twelve natural months');
    await page.locator('.usage-period-select').selectOption('custom');await ready();
    const inputs=page.locator('.usage-custom-range input');
    await inputs.nth(0).fill('2024-02-28');await inputs.nth(1).fill('2024-03-01');
    const originalLanguage=await page.locator('#fixture-locale').inputValue();
    await page.locator('#fixture-locale').selectOption(originalLanguage==='en'?'zh-CN':'en');await ready();
    if(await inputs.nth(0).inputValue()!=='2024-02-28'||await inputs.nth(1).inputValue()!=='2024-03-01')throw Error('Locale switch lost unapplied custom dates');
    await page.locator('.usage-custom-range button').click();await ready();
    if(await page.locator('.chart-column').count()!==3)throw Error('Custom dates lost leap day');
    const year=Number(await page.locator('.usage-calendar-controls>span').textContent());
    for(let i=year;i>2024;i--){await page.locator('.usage-calendar-controls button').first().click();await ready();}
    if(await page.locator('.usage-day').count()!==366)throw Error('Calendar year must include leap day');
    await page.locator('.usage-day[data-date="2024-02-29"]').click();await ready();
    if(await page.locator('.chart-column').count()!==1||await page.locator('.usage-range').getAttribute('data-start')!=='2024-02-29')throw Error('Selecting calendar date must update all statistics');
    const request=await page.evaluate(()=>window.usageFixture.requests.find(row=>row.start==='2024-02-29'&&row.end==='2024-02-29'));
    if(request.scope!=='main'||request.owner!=='main')throw Error('Date selection lost conversation owner');
    // Hold an older request while a different range completes, then release it.
    await page.evaluate(()=>window.usageFixture.hold());
    await page.locator('.usage-period-select').selectOption('week');
    await page.waitForFunction(()=>document.querySelector('.page-content')?.getAttribute('aria-busy')==='true');
    await page.locator('.usage-period-select').selectOption('month');await ready();
    const selected=await page.locator('.usage-range').getAttribute('data-start');
    await page.evaluate(()=>window.usageFixture.release());
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    await ready();
    if(await page.locator('.usage-range').getAttribute('data-start')!==selected)throw Error('Stale response replaced the selected range');
    await page.evaluate(()=>window.usageFixture.failCalendar(true));
    await page.locator('.usage-calendar-controls button').first().click();await ready();
    if(!await page.locator('.usage-calendar-error').count())throw Error('Annual failure must leave selected statistics available');
    await page.evaluate(()=>window.usageFixture.failCalendar(false));
    await page.locator('.usage-calendar-error button').click();await ready();
    if(!await page.locator('.usage-calendar').count())throw Error('Calendar retry failed');
    await page.locator('.usage-period-select').selectOption('recent');await ready();
    if(await page.locator('.chart-column').count()!==30)throw Error('Recent 30 days must remain available');
    await page.locator('.usage-period-select').selectOption('custom');await ready();
    await page.locator('.usage-custom-range input').nth(0).fill('2017-01-01');
    await page.locator('.usage-custom-range input').nth(1).fill('2026-12-31');
    await page.locator('.usage-custom-range button').click();await ready();
    if(await page.locator('.chart-column').count()!==120)throw Error('Long custom ranges must fit natural months without scrolling');
    await page.locator('.usage-period-select').selectOption('recent');await ready();
  }
  const language=await page.locator('#fixture-locale').inputValue(),selected=await page.locator('.usage-range').getAttribute('data-start');
  await page.locator('#fixture-locale').selectOption(language==='en'?'zh-CN':'en');
  await page.waitForFunction(expected=>document.querySelector('.usage-trend h2')?.textContent===expected,language==='en'?'用量趋势':'Usage trend');await ready();
  if(await page.locator('.usage-range').getAttribute('data-start')!==selected)throw Error('Language switch lost date selection');
  return 'Date controls, distributions, calendar selection, ownership, stale requests and locale passed';
}
