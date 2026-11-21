import { test, expect, PanelEditPage } from '@grafana/plugin-e2e';
import type { APIResponse, Page } from '@playwright/test';
import { shot } from './helpers';

const TYPE = 'smartquinoa1504-apachedrill-datasource';
const DS_NAME = 'Apache Drill';
// Never created: DROP ... IF EXISTS on it changes nothing even when allowed.
const TABLE = 'dfs.tmp.`grafana_safety_e2e`';

/** Put SQL in the query row's editor without running it (see queryEditor.spec.ts). */
async function pasteSql(page: Page, panelEditPage: PanelEditPage, sql: string) {
  const row = panelEditPage.getQueryEditorRow('A');
  await row.locator('.monaco-editor').first().click();
  await page.keyboard.press('ControlOrMeta+A');
  await row.locator('.monaco-editor textarea').first().evaluate((el, text) => {
    const dt = new DataTransfer();
    dt.setData('text/plain', text);
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
  }, sql);
}

/** Run one statement through Grafana's query API, as any API client could. */
function query(page: Page, uid: string, rawSql: string): Promise<APIResponse> {
  return page.request.post('/api/ds/query', {
    data: { from: 'now-1h', to: 'now', queries: [{ refId: 'A', datasource: { type: TYPE, uid }, rawSql, format: 'table' }] },
  });
}

test('by default a DROP is refused, with a warning in the editor before it is run', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  await pasteSql(page, panelEditPage, `DROP TABLE IF EXISTS ${TABLE}`);

  const warning = page.getByTestId('drill-statement-blocked');
  await expect(warning).toContainText('DROP statements are blocked');
  await expect(warning).toContainText('Statement safety');

  const response = page.waitForResponse(
    (r) => r.url().includes('/api/ds/query') && (r.request().postData() ?? '').includes('grafana_safety_e2e')
  );
  await page.keyboard.press('ControlOrMeta+Enter');
  const body = await (await response).json();
  expect(body.results.A.error).toContain('DROP statements are blocked');
  expect(body.results.A.error).not.toContain('grafana_safety_e2e');
  expect(body.results.A.status).toBe(403);
  expect(body.results.A.errorSource).toBe('downstream');
  await warning.scrollIntoViewIfNeeded();
  await expect(warning).toBeVisible();
  await shot(page, 'm6-safety', 'drop-blocked');

  // A query clears the warning, even one that mentions the keywords.
  await pasteSql(page, panelEditPage, "SELECT 'DROP TABLE x' AS s -- delete");
  await expect(warning).toBeHidden();
});

test('each setting allows its own kind of statement and nothing else', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  const uid = configPage.datasource.uid;
  await page.getByLabel('URL').fill('http://drill:8047');
  await expect(page.getByText('Statement safety')).toBeVisible();
  await expect(page.locator('#drill-allow-ddl')).not.toBeChecked();
  await expect(page.locator('#drill-allow-dml')).not.toBeChecked();
  await expect(page.locator('#drill-allow-options')).not.toBeChecked();
  await expect(configPage.saveAndTest()).toBeOK();

  // Nothing allowed: the API refuses every kind of write.
  for (const [sql, keyword] of [
    [`DROP TABLE IF EXISTS ${TABLE}`, 'DROP'],
    [`/* SELECT 1 */ drop table if exists ${TABLE}`, 'DROP'],
    [`INSERT INTO ${TABLE} SELECT 1`, 'INSERT'],
    ['ALTER SESSION SET `planner.slice_target` = 100000', 'ALTER SESSION'],
    ['USE dfs.tmp', 'USE'],
  ]) {
    const body = await (await query(page, uid, sql)).json();
    expect(body.results.A.error, sql).toContain(`${keyword} statements are blocked`);
  }
  const two = await (await query(page, uid, `SELECT 1; DROP TABLE IF EXISTS ${TABLE}`)).json();
  expect(two.results.A.error).toContain('Only one statement');
  expect((await query(page, uid, 'SELECT 1 AS one')).ok()).toBeTruthy();

  // Allow DDL only. Settings edits can be undone by Grafana's reload after
  // Save & test, so repeat until the saved setting takes effect.
  await page.waitForLoadState('networkidle');
  await expect(async () => {
    await page.locator('#drill-allow-ddl').check({ force: true });
    await expect(configPage.saveAndTest()).toBeOK();
    const dropped = await query(page, uid, `DROP TABLE IF EXISTS ${TABLE}`);
    expect(dropped.ok()).toBeTruthy();
    const frame = (await dropped.json()).results.A.frames[0];
    // Drill's own answer proves the statement reached it.
    expect(JSON.stringify(frame.data.values)).toContain('not found');
  }).toPass({ timeout: 30_000 });
  await shot(page, 'm6-safety', 'settings-ddl-allowed');

  const stillBlocked = await (await query(page, uid, 'ALTER SESSION SET `planner.slice_target` = 100000')).json();
  expect(stillBlocked.results.A.error).toContain('ALTER SESSION statements are blocked');
  const insert = await (await query(page, uid, `INSERT INTO ${TABLE} SELECT 1`)).json();
  expect(insert.results.A.error).toContain('INSERT statements are blocked');
});
