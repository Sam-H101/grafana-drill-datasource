import type { Page } from '@playwright/test';

/**
 * Save a full-page screenshot under artifacts/app-tests/<group>/<name>.png so a
 * person can check what the test saw.
 */
export async function shot(page: Page, group: string, name: string): Promise<void> {
  await page.screenshot({ path: `artifacts/app-tests/${group}/${name}.png`, fullPage: true });
}
