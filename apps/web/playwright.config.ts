import { defineConfig, devices } from '@playwright/test';

// Run through scripts/e2e.sh, which starts the real API (Postgres, Redis, Mailpit) first. The web
// dev server is started here so the Vite /api proxy is part of what is under test.
const webUrl = process.env.E2E_WEB_URL ?? 'http://localhost:5174';
const apiUrl = process.env.E2E_API_URL ?? 'http://127.0.0.1:8081';
const webPort = new URL(webUrl).port || '5174';

export default defineConfig({
	testDir: 'tests/e2e',
	testMatch: '**/*.spec.ts',
	// One worker: the API rate-limits per address and per connection, so tests must not race.
	fullyParallel: false,
	workers: 1,
	retries: 0,
	timeout: 60_000,
	reporter: [['list'], ['html', { open: 'never' }]],
	use: {
		baseURL: webUrl,
		// The installed Google Chrome; no browser download. CI installs its own browsers.
		...devices['Pixel 7'],
		viewport: { width: 375, height: 812 },
		channel: 'chrome',
		trace: 'retain-on-failure'
	},
	webServer: {
		command: `npm run dev -- --port ${webPort} --strictPort`,
		url: webUrl,
		reuseExistingServer: false,
		timeout: 120_000,
		env: { API_PROXY_TARGET: apiUrl }
	}
});
