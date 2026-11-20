import { test, expect } from '@grafana/plugin-e2e';

// Regenerates the screenshots in src/img/screenshots used by the README and
// plugin.json. Not part of the normal suite:
//   devenv/run.sh bash -c 'DOCS_SCREENSHOTS=1 npx playwright test tests/e2e/docs-screenshots.spec.ts'
test.skip(!process.env.DOCS_SCREENSHOTS, 'set DOCS_SCREENSHOTS=1 to regenerate documentation screenshots');
test.use({ viewport: { width: 1440, height: 900 } });

const OUT = 'src/img/screenshots';

test('dashboard', async ({ gotoDashboardPage, page }) => {
  const dashboard = await gotoDashboardPage({ uid: 'drill-demo' });
  await expect(dashboard.getPanelByTitle('Hires per quarter by gender').locator).toContainText('hires F');
  await expect(dashboard.getPanelByTitle('Employees in Store Management').locator).toContainText('full_name');
  await page.waitForLoadState('networkidle');
  await page.screenshot({ path: `${OUT}/dashboard.png` });
});

test('query editor', async ({ gotoPanelEditPage, page }) => {
  const panelEdit = await gotoPanelEditPage({ dashboard: { uid: 'drill-demo' }, id: '1' });
  await expect(panelEdit.panel.locator).toContainText('hires F');
  await page.waitForLoadState('networkidle');
  await page.screenshot({ path: `${OUT}/query-editor.png` });
});

test('config editor', async ({ gotoDataSourceConfigPage, page }) => {
  const configPage = await gotoDataSourceConfigPage('drill-dev');
  await expect(configPage.saveAndTest()).toBeOK();
  await page.getByText('Connection', { exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: `${OUT}/config-editor.png` });
});
