import { test, expect, PanelEditPage } from '@grafana/plugin-e2e';
import type { Page, Response } from '@playwright/test';
import { shot } from './helpers';

const DS_NAME = 'Apache Drill';

/** Replace the SQL in the query row's editor and run it with Ctrl+Enter. */
async function runSql(page: Page, panelEditPage: PanelEditPage, sql: string): Promise<Response> {
  const row = panelEditPage.getQueryEditorRow('A');
  const editor = row.locator('.monaco-editor').first();
  await editor.click();
  await page.keyboard.press('ControlOrMeta+A');
  // Paste rather than type: typing triggers Monaco's bracket auto-closing and
  // auto-indent, which change the SQL. The page is not a secure context, so
  // the clipboard API is unavailable; dispatch a paste event instead.
  await row.locator('.monaco-editor textarea').first().evaluate((el, text) => {
    const dt = new DataTransfer();
    dt.setData('text/plain', text);
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
  }, sql);
  // Match on the first line only: Monaco sends \r\n line endings. The body is
  // JSON, so the line appears as JSON.stringify writes it (minus the end quote).
  const firstLine = JSON.stringify(sql.split('\n')[0]).slice(0, -1);
  const marker = `"rawSql":${firstLine}`;
  const response = page.waitForResponse(
    (r) => r.url().includes('/api/ds/query') && (r.request().postData() ?? '').includes(marker)
  );
  await page.keyboard.press('ControlOrMeta+Enter');
  return response;
}

test('table query returns rows from Drill', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  await panelEditPage.setVisualization('Table');
  const resp = await runSql(page, panelEditPage, 'SELECT employee_id, full_name, salary FROM cp.`employee.json` ORDER BY employee_id;');
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  const frame = body.results.A.frames[0];
  expect(frame.schema.fields.map((f: { name: string }) => f.name)).toEqual(['employee_id', 'full_name', 'salary']);
  expect(frame.data.values[1]).toContain('Sheri Nowmer');
  await expect(panelEditPage.panel.locator).toContainText('Sheri Nowmer');
  await shot(page, 'm2-query', 'table');
});

test('time series query: one series per group, dashboard range applied, expanded SQL visible', async ({
  panelEditPage,
  page,
}) => {
  await panelEditPage.datasource.set(DS_NAME);
  await panelEditPage.setVisualization('Time series');
  await panelEditPage.timeRange.set({ from: '1994-01-01 00:00:00', to: '1998-12-31 23:59:59' });

  const row = panelEditPage.getQueryEditorRow('A');
  await row.getByRole('radio', { name: 'Time series' }).check({ force: true });

  const sql = [
    'SELECT $__timeGroupAlias(CAST(hire_date AS TIMESTAMP), 90d), gender, COUNT(*) AS hires',
    'FROM cp.`employee.json`',
    'WHERE $__timeFilter(CAST(hire_date AS TIMESTAMP))',
    'GROUP BY 1, 2 ORDER BY 1',
  ].join('\n');
  const resp = await runSql(page, panelEditPage, sql);
  expect(resp.ok()).toBeTruthy();
  const body = await resp.json();
  const frames = body.results.A.frames;
  const frame = frames[0];

  // Wide frame: time plus one field per gender.
  const labels = frame.schema.fields.slice(1).map((f: { labels?: Record<string, string> }) => f.labels?.gender).sort();
  expect(labels).toEqual(['F', 'M']);
  const executed: string = frame.schema.meta.executedQueryString;
  expect(executed).toContain("TIMESTAMP '1994-01-01 00:00:00.000'");
  expect(executed).toContain('EXTRACT(EPOCH FROM CAST(hire_date AS TIMESTAMP)) / 7776000000.0');
  expect(executed).not.toContain('$__');
  // Every point lies inside the dashboard range.
  const times: number[] = frame.data.values[0];
  expect(times.length).toBeGreaterThan(3);
  for (const t of times) {
    expect(t).toBeGreaterThanOrEqual(Date.UTC(1993, 9, 1)); // bucket start may precede the range
    expect(t).toBeLessThanOrEqual(Date.UTC(1999, 0, 1));
  }

  await expect(panelEditPage.panel.locator.getByText('hires F').or(panelEditPage.panel.locator.getByText('F'))).toBeVisible();
  await shot(page, 'm2-query', 'timeseries');

  // Query inspector shows the SQL that ran.
  await page.getByRole('button', { name: 'Query inspector' }).click();
  await page.getByRole('tab', { name: 'Query' }).click();
  await page.getByRole('button', { name: 'Refresh' }).click();
  await expect(page.getByText("TIMESTAMP '1994-01-01 00:00:00.000'").first()).toBeVisible();
  await shot(page, 'm2-query', 'inspector');
});

test('a Drill error is shown with its message and flagged as downstream', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  const resp = await runSql(page, panelEditPage, 'SELEC 1');
  const body = await resp.json();
  expect(body.results.A.error).toContain('PARSE ERROR');
  expect(body.results.A.error).not.toContain('grafana-query');
  expect(body.results.A.errorSource).toBe('downstream');
  const message = page.getByText(/PARSE ERROR/).first();
  await message.scrollIntoViewIfNeeded();
  await expect(message).toBeVisible();
  await shot(page, 'm2-query', 'drill-error');
});

test('macro mistakes are reported before reaching Drill', async ({ panelEditPage, page }) => {
  await panelEditPage.datasource.set(DS_NAME);
  const resp = await runSql(page, panelEditPage, 'SELECT * FROM cp.`employee.json` WHERE $__timeFilter(hire_date');
  const body = await resp.json();
  expect(body.results.A.error).toContain('macro $__timeFilter: missing closing parenthesis');
  await expect(page.getByText('missing closing parenthesis').first()).toBeVisible();
});

test('editor suggests schemas and tables from Drill, and the schema picker lists schemas', async ({
  panelEditPage,
  page,
}) => {
  await panelEditPage.datasource.set(DS_NAME);
  const row = panelEditPage.getQueryEditorRow('A');
  await row.locator('.monaco-editor').first().click();
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.press('Delete');

  // After "FROM sys." the tables of the sys schema are offered.
  await page.keyboard.type('SELECT * FROM sys.');
  const suggestions = page.locator('.suggest-widget');
  await expect(suggestions).toBeVisible();
  await expect(suggestions.getByText('drillbits', { exact: true })).toBeVisible();
  await shot(page, 'm3-editor', 'table-suggestions');
  await page.keyboard.press('Escape');

  // Elsewhere, schema names are offered.
  await page.keyboard.press('ControlOrMeta+A');
  await page.keyboard.type('SELECT * FROM df');
  await page.keyboard.press('Control+Space');
  await expect(suggestions.getByText('dfs.tmp', { exact: true })).toBeVisible();
  await page.keyboard.press('Escape');

  // The schema picker lists schemas too.
  await row.locator('#drill-query-schema').click();
  await expect(page.getByRole('option', { name: 'dfs.tmp' })).toBeVisible();
  await shot(page, 'm3-editor', 'schema-picker');
});
