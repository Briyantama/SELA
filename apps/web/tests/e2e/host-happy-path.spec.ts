import { randomBytes } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { expect, test, type Page } from '@playwright/test';
import jsQR from 'jsqr';
import { PNG } from 'pngjs';
import { waitForOtp } from './support/mailpit';
import { resetRateLimits } from './support/redis';
import { createStepTimer } from './support/timing';

// PRD success metric: a host finishes setup in 3 minutes or less. This run measures a scripted user,
// so it is a lower bound; the human sessions are tracked separately (docs/tracking, T7.2).
const TARGET_MS = 180_000;

const API_URL = process.env.E2E_API_URL ?? 'http://127.0.0.1:8081';
const EVENT_NAME = 'Pernikahan Sari & Budi';
const EVENT_DATE = '2026-12-05';
const WEB_URL = (process.env.E2E_WEB_URL ?? 'http://localhost:5174').replace(/\/$/, '');
// SHORT_LINK_BASE_URL is the web origin, so a short link is {web}/e/{8-char base62 code}.
const SHORT_LINK = new RegExp(`^${WEB_URL.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}/e/[0-9A-Za-z]{8}$`);

const uniqueEmail = () => `host-${Date.now()}-${randomBytes(3).toString('hex')}@example.test`;

test.beforeEach(() => {
	resetRateLimits();
});

/** From the login page's email step: request the code, read it from Mailpit, sign in. */
async function completeSignIn(page: Page, email: string) {
	await page.getByLabel('Email').fill(email);
	await page.getByRole('button', { name: 'Kirim kode' }).click();
	const code = await waitForOtp(email);
	await page.getByLabel('Kode 6 digit').fill(code);
	await page.getByRole('button', { name: 'Masuk' }).click();
}

async function chooseCategoryAndFill(page: Page) {
	await page.getByRole('radio', { name: /Pernikahan/ }).check();
	await page.getByRole('button', { name: 'Lanjut' }).click();
	await page.getByLabel('Nama acara').fill(EVENT_NAME);
	await page.getByLabel('Tanggal acara').fill(EVENT_DATE);
}

test('a host signs in, creates an event and gets a working short link and QR code', async ({
	page,
	context,
	request
}, testInfo) => {
	const timer = createStepTimer();

	await timer.step('sign in', async () => {
		await page.goto('/auth/login');
		await completeSignIn(page, uniqueEmail());
		await expect(page).toHaveURL(/\/events\/new$/);
	});
	await timer.step('choose category', async () => {
		await page.getByRole('radio', { name: /Pernikahan/ }).check();
		await page.getByRole('button', { name: 'Lanjut' }).click();
	});
	await timer.step('fill details', async () => {
		await page.getByLabel('Nama acara').fill(EVENT_NAME);
		await page.getByLabel('Tanggal acara').fill(EVENT_DATE);
	});
	await timer.step('create event', async () => {
		await page.getByRole('button', { name: 'Buat acara' }).click();
		await expect(page.getByRole('heading', { level: 1, name: EVENT_NAME })).toBeVisible();
		await expect(page.getByRole('img', { name: `Kode QR untuk ${EVENT_NAME}` })).toBeVisible();
	});

	// The result screen.
	const eventId = /\/events\/([0-9a-f-]{36})$/.exec(page.url())?.[1];
	expect(eventId, 'the page URL carries the new event id').toBeTruthy();
	await expect(page.getByText('Sabtu, 5 Desember 2026')).toBeVisible();
	const link = (await page.getByText(SHORT_LINK).textContent()) ?? '';
	expect(link).toMatch(SHORT_LINK);
	const code = link.slice(link.lastIndexOf('/') + 1);

	// The QR image really loads, from the host-only endpoint, with the session cookie.
	const qr = page.getByRole('img', { name: `Kode QR untuk ${EVENT_NAME}` });
	await expect
		.poll(() => qr.evaluate((el: HTMLImageElement) => el.complete && el.naturalWidth > 0), {
			message: 'the QR image finished loading'
		})
		.toBe(true);
	const svgResponse = await page.request.get(`/api/v1/events/${eventId}/qr.svg`);
	expect(svgResponse.status()).toBe(200);
	expect(svgResponse.headers()['content-type']).toContain('image/svg+xml');

	// PNG download: right name, a real PNG, and it scans back to exactly the short link.
	const [pngDownload] = await Promise.all([
		page.waitForEvent('download'),
		page.getByRole('link', { name: 'Unduh PNG' }).click()
	]);
	expect(pngDownload.suggestedFilename()).toBe(`sela-${code}.png`);
	const png = await readFile((await pngDownload.path()) as string);
	expect(png.subarray(0, 4)).toEqual(Buffer.from([0x89, 0x50, 0x4e, 0x47]));
	const bitmap = PNG.sync.read(png);
	const decoded = jsQR(new Uint8ClampedArray(bitmap.data), bitmap.width, bitmap.height);
	expect(decoded?.data, 'the QR image decodes to the short link').toBe(link);

	// SVG download.
	const [svgDownload] = await Promise.all([
		page.waitForEvent('download'),
		page.getByRole('link', { name: 'Unduh SVG' }).click()
	]);
	expect(svgDownload.suggestedFilename()).toBe(`sela-${code}.svg`);
	expect(await readFile((await svgDownload.path()) as string, 'utf8')).toContain('<svg');

	// The session is an HttpOnly cookie that script cannot read.
	const session = (await context.cookies()).find((c) => c.name === 'sela_session');
	expect(session?.httpOnly).toBe(true);
	expect(session?.sameSite).toBe('Lax');
	expect(await page.evaluate(() => document.cookie)).not.toContain('sela_session');

	// A guest (no cookie) resolves the short code to guest-safe metadata only.
	const guest = await request.get(`${API_URL}/e/${code}`);
	expect(guest.status()).toBe(200);
	const guestBody = await guest.text();
	expect(JSON.parse(guestBody).data.event_id).toBe(eventId);
	for (const secret of ['host_id', 'access_token', 'package']) {
		expect(guestBody).not.toContain(secret);
	}

	// Timing against the 3-minute target.
	const report = timer.report();
	// Next to the other Playwright artifacts (apps/web/test-results), whatever the working directory is.
	const timingFile = join(testInfo.project.outputDir, 'timing.json');
	await mkdir(testInfo.project.outputDir, { recursive: true });
	await writeFile(
		timingFile,
		JSON.stringify({ ...report, targetMs: TARGET_MS, measuredAt: new Date().toISOString() }, null, 2)
	);
	await testInfo.attach('timing', { body: JSON.stringify(report), contentType: 'application/json' });
	console.log(
		`e2e timing: ${report.steps.map((s) => `${s.name} ${s.ms}ms`).join(', ')}; total ${report.totalMs}ms (target ${TARGET_MS}ms)`
	);
	expect(report.totalMs).toBeLessThan(TARGET_MS);
});

test('a signed-out host is sent to sign in and lands back on the new-event page', async ({ page }) => {
	await page.goto('/events/new');
	await chooseCategoryAndFill(page);

	await page.getByRole('button', { name: 'Buat acara' }).click();

	await expect(page).toHaveURL(/\/auth\/login\?next=%2Fevents%2Fnew$/);
	await completeSignIn(page, uniqueEmail());
	await expect(page).toHaveURL(/\/events\/new$/);
});

test('the event page survives a reload because the session cookie persists', async ({ page }) => {
	await page.goto('/auth/login');
	await completeSignIn(page, uniqueEmail());
	await chooseCategoryAndFill(page);
	await page.getByRole('button', { name: 'Buat acara' }).click();
	await expect(page.getByRole('heading', { level: 1, name: EVENT_NAME })).toBeVisible();

	await page.reload();

	await expect(page.getByRole('heading', { level: 1, name: EVENT_NAME })).toBeVisible();
	await expect(page.getByText(SHORT_LINK)).toBeVisible();
});

test('an event id that does not exist shows the not-found message', async ({ page }) => {
	await page.goto('/auth/login');
	await completeSignIn(page, uniqueEmail());
	await expect(page).toHaveURL(/\/events\/new$/);

	await page.goto('/events/00000000-0000-4000-8000-000000000000');

	await expect(page.getByRole('alert')).toHaveText('Acara tidak ditemukan.');
});
