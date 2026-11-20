import { test, expect } from '@grafana/plugin-e2e';
import { shot } from './helpers';

test('plugin is installed and listed', async ({ page }) => {
  await page.goto('/plugins/smartquinoa1504-apachedrill-datasource');
  await expect(page.getByRole('heading', { name: 'Apache Drill', exact: true })).toBeVisible();
  await shot(page, 'smoke', 'plugin-page');
});
