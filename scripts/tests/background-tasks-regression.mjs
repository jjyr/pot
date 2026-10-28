async (page, options) => {
  await page.goto(options.origin + '/scripts/tests/fixtures/background-tasks-regression.html');
  await page.waitForFunction(() => typeof window.runBackgroundTaskRegression === 'function');
  return page.evaluate(async () => await window.runBackgroundTaskRegression());
}
