import { test, expect } from '@grafana/plugin-e2e';
import { shot } from './helpers';

const TYPE = 'smartquinoa1504-apachedrill-datasource';
const PRIVATE_URL = 'http://drill-private:8047';

// The dev stack's Grafana has the secure socks proxy turned on, pointing at a
// plain SOCKS5 proxy (docker-compose.yaml). That is the mechanism behind
// Grafana Cloud's Private data source connect. "drill-private" resolves only
// on a network Grafana is not on, so a request that reaches it went through
// the proxy.

test('a private Drill is reachable only with the secure socks proxy on', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await page.getByLabel('URL').fill(PRIVATE_URL);

  // Directly, Grafana cannot even resolve the name.
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: `Cannot reach Drill at ${PRIVATE_URL}: host not found.` });
  await shot(page, 'private-network', 'direct-fails');

  // With the proxy on, the same settings connect. The switch is Grafana's own
  // control for this; repeat the edit until the saved setting takes effect.
  const proxySwitch = page.getByTestId('drill-secure-socks').getByRole('switch');
  await page.waitForLoadState('networkidle');
  await expect(async () => {
    if (!(await proxySwitch.isChecked())) {
      await proxySwitch.check({ force: true });
    }
    await expect(configPage.saveAndTest()).toBeOK();
  }).toPass({ timeout: 45_000 });
  await expect(configPage).toHaveAlert('success', { hasText: /Connected to Apache Drill 1\.\d+/ });
  await shot(page, 'private-network', 'proxy-ok');
});

test('queries on the provisioned private data source go through the proxy', async ({ page }) => {
  const health = await page.request.get('/api/datasources/uid/drill-pdc/health');
  expect(health.ok()).toBeTruthy();
  expect((await health.json()).message).toContain('Connected to Apache Drill');

  const resp = await page.request.post('/api/ds/query', {
    data: {
      from: 'now-1h',
      to: 'now',
      queries: [{ refId: 'A', datasource: { type: TYPE, uid: 'drill-pdc' }, rawSql: 'SELECT 41 + 1 AS answer', format: 'table' }],
    },
  });
  expect(resp.ok()).toBeTruthy();
  const frame = (await resp.json()).results.A.frames[0];
  expect(frame.schema.fields[0].name).toBe('answer');
  expect(frame.data.values[0]).toEqual([42]);
});
