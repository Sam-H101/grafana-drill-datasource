import { test, expect } from '@grafana/plugin-e2e';
import { existsSync, readFileSync } from 'node:fs';
import { shot } from './helpers';

const TYPE = 'smartquinoa1504-apachedrill-datasource';
const CA_FILE = process.env.DRILL_TLS_CA_FILE ?? '/tls/ca.crt';

test('HTTPS Drill: untrusted until its CA certificate is configured', async ({ createDataSourceConfigPage, page }) => {
  test.skip(!existsSync(CA_FILE), `no CA certificate at ${CA_FILE} (set DRILL_TLS_CA_FILE)`);
  const ca = readFileSync(CA_FILE, 'utf8');

  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await page.getByLabel('URL').fill('https://drill-tls:8047');
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'certificate signed by unknown authority' });
  await shot(page, 'm4-hardening', 'tls-untrusted');

  await page.waitForLoadState('networkidle');
  await page.locator('#drill-tls-with-ca').check({ force: true });
  await page.locator('#drill-tls-ca').fill(ca);
  await expect(configPage.saveAndTest()).toBeOK();
  await expect(configPage).toHaveAlert('success', { hasText: 'Connected to Apache Drill' });
  await shot(page, 'm4-hardening', 'tls-custom-ca');
});

// Run by devenv/app-test-drill-failure.sh, which stops the drill container
// while this query is running. Skipped otherwise.
test('Drill stopping mid-query gives a clean error in the panel', async ({ panelEditPage, page }) => {
  test.skip(!process.env.DRILL_KILL_TEST, 'run through devenv/app-test-drill-failure.sh');
  test.setTimeout(120_000);
  await panelEditPage.datasource.set('Apache Drill');
  const row = panelEditPage.getQueryEditorRow('A');
  await row.locator('.monaco-editor').first().click();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.insertText(
    'SELECT COUNT(*) AS n FROM cp.`employee.json` a JOIN cp.`employee.json` b ON a.gender = b.gender ' +
      'JOIN cp.`employee.json` c ON b.gender = c.gender JOIN cp.`employee.json` d ON c.gender = d.gender'
  );
  const started = Date.now();
  const response = page.waitForResponse((r) => r.url().includes('/api/ds/query'), { timeout: 90_000 });
  await page.keyboard.press('ControlOrMeta+Enter');

  const body = await (await response).json();
  const elapsed = (Date.now() - started) / 1000;
  const error: string = body.results.A.error;
  expect(error).toMatch(/closed the connection before answering|Cannot reach Drill/);
  expect(body.results.A.errorSource).toBe('downstream');
  // Well inside the 60 s query timeout.
  expect(elapsed).toBeLessThan(55);
  const message = page.getByText(/closed the connection|Cannot reach Drill/).first();
  await message.scrollIntoViewIfNeeded();
  await expect(message).toBeVisible();
  await shot(page, 'm4-hardening', 'drill-stopped');
  console.log(`error after ${elapsed.toFixed(1)}s: ${error}`);
});
