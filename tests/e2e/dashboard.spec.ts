import { test, expect } from '@grafana/plugin-e2e';
import type { Page, Response } from '@playwright/test';
import { shot } from './helpers';

type QueryBody = { queries: Array<{ refId: string; rawSql?: string }> };

/** Collect /api/ds/query responses whose request contains `needle`. */
function collect(page: Page, needle: string) {
  const seen: Response[] = [];
  page.on('response', (r) => {
    if (r.url().includes('/api/ds/query') && (r.request().postData() ?? '').includes(needle)) {
      seen.push(r);
    }
  });
  return seen;
}

test('provisioned dashboard: variables, annotations, table, time series and stat all render', async ({
  gotoDashboardPage,
  page,
}) => {
  const tableResponses = collect(page, 'department_id = ');
  const annoResponses = collect(page, 'Senior Management');

  const dashboard = await gotoDashboardPage({ uid: 'drill-demo' });

  // Variables loaded their options from Drill.
  await expect(page.getByText('Gender', { exact: true }).first()).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText('Store Management').first()).toBeVisible();

  // Panels show data, not errors.
  const ts = dashboard.getPanelByTitle('Hires per quarter by gender');
  await expect(ts.locator).toContainText('hires F');
  await expect(ts.locator).toContainText('hires M');
  const table = dashboard.getPanelByTitle('Employees in Store Management');
  await table.locator.scrollIntoViewIfNeeded(); // Grafana only queries panels in view
  await expect(table.locator).toContainText('full_name');
  const stat = dashboard.getPanelByTitle('Employees by gender');
  await expect(stat.locator).toContainText('F');
  await expect(page.locator('[data-testid*="Panel status error"]')).toHaveCount(0);

  // The multi-value "All" selection was expanded into a quoted IN list.
  await expect.poll(() => tableResponses.length, { timeout: 15_000 }).toBeGreaterThan(0);
  const req = tableResponses[0].request().postDataJSON() as QueryBody;
  const sql = req.queries[0].rawSql ?? '';
  expect(sql).toContain("gender IN ('F','M')");
  expect(sql).toContain('department_id = 11');
  expect(tableResponses[0].ok()).toBeTruthy();

  // The annotation query ran and returned events.
  await expect.poll(() => annoResponses.length).toBeGreaterThan(0);
  const anno = await annoResponses[0].json();
  const annoFrame = anno.results.Anno.frames[0];
  expect(annoFrame.schema.fields.map((f: { name: string }) => f.name)).toEqual(['time', 'text', 'tags']);
  expect(annoFrame.data.values[0].length).toBeGreaterThan(0);

  await shot(page, 'm3-dashboard', 'all-genders');
});

test('choosing one gender re-runs the queries with a single quoted value', async ({ gotoDashboardPage, page }) => {
  const tableResponses = collect(page, 'department_id = ');
  const dashboard = await gotoDashboardPage({ uid: 'drill-demo', queryParams: new URLSearchParams({ 'var-gender': 'F' }) });
  // Grafana only queries panels in view.
  await dashboard.getPanelByTitle('Employees in Store Management').locator.scrollIntoViewIfNeeded();
  await expect.poll(() => tableResponses.length, { timeout: 15_000 }).toBeGreaterThan(0);
  const last = tableResponses[tableResponses.length - 1];
  const sql = (last.request().postDataJSON() as QueryBody).queries[0].rawSql ?? '';
  expect(sql).toContain("gender IN ('F')");
  const body = await last.json();
  const genders: string[] = body.results.A.frames[0].data.values[2];
  expect(genders.length).toBeGreaterThan(0);
  expect(new Set(genders)).toEqual(new Set(['F']));
  await shot(page, 'm3-dashboard', 'gender-F');
});

test('variable editor previews values, using __text and __value', async ({ variableEditPage, page }) => {
  await variableEditPage.setVariableType('Query');
  await variableEditPage.datasource.set('Apache Drill');
  const sql = page.locator('#drill-variable-sql');
  await sql.fill('SELECT department_id AS __value, department_description AS __text FROM cp.`department.json` ORDER BY 1');
  await sql.blur();
  await variableEditPage.runQuery();
  await expect(variableEditPage).toDisplayPreviews(['HQ General Management', 'HQ Information Systems']);
  await shot(page, 'm3-dashboard', 'variable-editor');
});

test('provisioned alert rule evaluates without error', async ({ page }) => {
  test.setTimeout(150_000);
  let rule: { health?: string; state?: string; lastError?: string } | undefined;
  await expect
    .poll(
      async () => {
        const resp = await page.request.get('/api/prometheus/grafana/api/v1/rules');
        const body = await resp.json();
        for (const g of body.data?.groups ?? []) {
          for (const r of g.rules ?? []) {
            if (r.name === 'Drill sample data is present') {
              rule = r;
            }
          }
        }
        // Wait until the rule has been evaluated at least once.
        return rule && rule.health !== 'unknown' && rule.state ? rule.health : 'pending';
      },
      { timeout: 120_000, intervals: [5_000] }
    )
    .toBe('ok');
  expect(rule?.lastError ?? '').toBe('');
  expect(rule?.state).toBe('inactive'); // the sample data is there, so the rule does not fire
  await page.goto('/alerting/list?search=drill');
  await expect(page.getByText('Drill sample data is present').first()).toBeVisible({ timeout: 20_000 });
  await shot(page, 'm3-dashboard', 'alert-rule');
});
