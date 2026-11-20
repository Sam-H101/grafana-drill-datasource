import { test, expect } from '@grafana/plugin-e2e';
import type { Page } from '@playwright/test';
import { shot } from './helpers';

const TYPE = 'smartquinoa1504-apachedrill-datasource';
// Dev-only credentials (devenv/drill/README.md). Grafana reaches the
// containers by their compose service names.
const AUTH_URL = 'http://drill-auth:8047';
const AUTH_USER = 'grafana';
const AUTH_PASS = 'grafana-dev-password';

async function fillConnection(page: Page, url: string) {
  await page.getByLabel('URL').fill(url);
}

async function chooseAuth(page: Page, mode: 'None' | 'Basic' | 'Form login') {
  await page.getByRole('radio', { name: mode }).check({ force: true });
}

test('provisioned data source: Save & test succeeds and reports the Drill version', async ({
  gotoDataSourceConfigPage,
  page,
}) => {
  const configPage = await gotoDataSourceConfigPage('drill-dev');
  // The first page load after Grafana starts can take more than 5 s.
  await expect(page.getByLabel('URL')).toHaveValue('http://drill:8047', { timeout: 15_000 });
  await expect(configPage.saveAndTest()).toBeOK();
  await expect(configPage).toHaveAlert('success', { hasText: /Connected to Apache Drill 1\.\d+/ });
  await shot(page, 'm1-config', 'provisioned-ok');
});

test('unreachable URL gives a clear error', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await fillConnection(page, 'http://drill:9999');
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'Cannot reach Drill at http://drill:9999' });
  await shot(page, 'm1-config', 'unreachable');
});

test('Drill with authentication but none configured', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await fillConnection(page, AUTH_URL);
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'Drill requires authentication' });
});

test('wrong password is reported, right password works (Basic)', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await fillConnection(page, AUTH_URL);
  await chooseAuth(page, 'Basic');
  await page.getByLabel('Username').fill(AUTH_USER);
  await page.locator('#drill-password').fill('not-the-password');
  await expect(page.getByText('Credentials over plain HTTP')).toBeVisible();
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'Drill rejected the credentials' });
  await shot(page, 'm1-config', 'wrong-password');

  // The saved password is now write-only: reset it and enter the right one.
  // Grafana reloads the saved settings after Save & test; editing before that
  // finishes gets overwritten, so wait for the page to settle.
  await page.waitForLoadState('networkidle');
  const password = page.locator('#drill-password');
  await expect(password).toBeDisabled();
  await page.getByRole('button', { name: 'Reset' }).first().click();
  await expect(password).toBeEditable();
  await password.fill(AUTH_PASS);
  await expect(configPage.saveAndTest()).toBeOK();
  await expect(configPage).toHaveAlert('success', { hasText: 'Connected to Apache Drill' });
  await shot(page, 'm1-config', 'basic-ok');
});

test('form login works', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await fillConnection(page, AUTH_URL);
  await chooseAuth(page, 'Form login');
  await page.getByLabel('Username').fill(AUTH_USER);
  await page.locator('#drill-password').fill(AUTH_PASS);
  await expect(configPage.saveAndTest()).toBeOK();
  await expect(configPage).toHaveAlert('success', { hasText: 'Connected to Apache Drill' });
});

test('invalid settings are explained', async ({ createDataSourceConfigPage, page }) => {
  const configPage = await createDataSourceConfigPage({ type: TYPE });
  await fillConnection(page, 'http://user:pw@drill:8047');
  await expect(configPage.saveAndTest()).not.toBeOK();
  await expect(configPage).toHaveAlert('error', { hasText: 'not in the URL' });
});
