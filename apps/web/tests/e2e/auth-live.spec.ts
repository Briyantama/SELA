import { randomBytes } from 'node:crypto';
import { expect, request as playwrightRequest, test, type APIRequestContext } from '@playwright/test';
import { waitForOtp, waitForOtpMessage } from './support/mailpit';
import { dumpKeys, resetRateLimits } from './support/redis';

// Live verification of the email OTP flow against the real stack: the real API, Redis (with a
// password) and Mailpit over SMTP. Until now this flow had only run against miniredis and a fake
// SMTP server (tracker T3.8, FR-09.1).

const API = process.env.E2E_API_URL ?? 'http://127.0.0.1:8081';
const REQUEST_OTP = `${API}/api/v1/auth/otp/request`;
const VERIFY_OTP = `${API}/api/v1/auth/otp/verify`;

const uniqueEmail = () => `live-${Date.now()}-${randomBytes(3).toString('hex')}@example.test`;
const wrongCodeFor = (code: string) => (code === '000000' ? '111111' : '000000');

test.beforeEach(() => {
	resetRateLimits();
});

async function requestCode(request: APIRequestContext, email: string) {
	const response = await request.post(REQUEST_OTP, { data: { email } });
	expect(response.status(), 'OTP request accepted').toBe(200);
	return waitForOtp(email);
}

const verify = (request: APIRequestContext, email: string, code: string) =>
	request.post(VERIFY_OTP, { data: { email, code } });

test('delivers the sign-in code by email through the real SMTP path', async ({ request }) => {
	const email = uniqueEmail();

	const response = await request.post(REQUEST_OTP, { data: { email } });
	const mail = await waitForOtpMessage(email);

	expect(response.status()).toBe(200);
	expect((await response.json()).data.expires_in_seconds).toBe(300);
	expect(mail.from).toBe('no-reply@sela.local');
	expect(mail.subject).toBe('Kode masuk Sela');
	expect(mail.code).toMatch(/^\d{6}$/);
	expect(mail.text).toContain('Kode berlaku 5 menit');
});

test('rejects a wrong code, then accepts the right one with a safe session cookie', async ({ request }) => {
	const email = uniqueEmail();
	const code = await requestCode(request, email);

	const wrong = await verify(request, email, wrongCodeFor(code));
	const right = await verify(request, email, code);

	expect(wrong.status()).toBe(401);
	expect((await wrong.json()).error).toBe('invalid or expired code');
	expect(right.status()).toBe(200);

	const body = await right.text();
	const setCookie = right.headers()['set-cookie'] ?? '';
	expect(setCookie).toMatch(/^sela_session=[^;]+/);
	expect(setCookie).toContain('HttpOnly');
	expect(setCookie).toContain('SameSite=Lax');
	expect(setCookie).toMatch(/Max-Age=\d+/);
	expect(setCookie).not.toContain('Secure'); // COOKIE_SECURE=false for plain-HTTP local runs
	const token = /sela_session=([^;]+)/.exec(setCookie)?.[1] ?? '';
	expect(body, 'the session token never appears in the response body').not.toContain(token);
	expect(JSON.parse(body).data.host_id).toBeTruthy();

	// The cookie now authenticates this client (404 = signed in but no such event); a client without it gets 401.
	const missingEvent = '00000000-0000-4000-8000-000000000000';
	expect((await request.get(`${API}/api/v1/events/${missingEvent}`)).status()).toBe(404);
	const anonymous = await playwrightRequest.newContext();
	expect((await anonymous.get(`${API}/api/v1/events/${missingEvent}`)).status()).toBe(401);
	await anonymous.dispose();
});

test('a code can be used only once', async ({ request }) => {
	const email = uniqueEmail();
	const code = await requestCode(request, email);

	const first = await verify(request, email, code);
	const second = await verify(request, email, code);

	expect(first.status()).toBe(200);
	expect(second.status()).toBe(401);
});

test('Redis keeps only hashes: no plaintext code and no plaintext session token', async ({ request }) => {
	const email = uniqueEmail();
	const code = await requestCode(request, email);

	const issued = dumpKeys('otp:*');
	expect(Object.keys(issued).length, 'an OTP entry exists, so this check is not vacuous').toBeGreaterThan(0);
	expect(JSON.stringify(issued)).not.toContain(code);

	const verified = await verify(request, email, code);
	const token = /sela_session=([^;]+)/.exec(verified.headers()['set-cookie'] ?? '')?.[1] ?? '';
	const sessions = dumpKeys('session:*');
	expect(token).not.toBe('');
	expect(Object.keys(sessions).length, 'a session entry exists').toBeGreaterThan(0);
	expect(JSON.stringify(sessions)).not.toContain(token);
});

test('rate limits repeated code requests for one address', async ({ request }) => {
	const email = uniqueEmail();

	const statuses: number[] = [];
	let last = await request.post(REQUEST_OTP, { data: { email } });
	statuses.push(last.status());
	for (let i = 0; i < 3; i++) {
		last = await request.post(REQUEST_OTP, { data: { email } });
		statuses.push(last.status());
	}

	expect(statuses).toEqual([200, 200, 200, 429]);
	expect(Number(last.headers()['retry-after'])).toBeGreaterThan(0);
	expect((await last.json()).error).toBe('too many requests');
});

test('locks the address after five wrong codes, even for the right code', async ({ request }) => {
	const email = uniqueEmail();
	const code = await requestCode(request, email);

	const wrongStatuses: number[] = [];
	for (let i = 0; i < 5; i++) {
		wrongStatuses.push((await verify(request, email, wrongCodeFor(code))).status());
	}
	const locked = await verify(request, email, code);

	// The first four wrong codes are plain rejections; the fifth may already report the lockout it causes.
	expect(wrongStatuses.slice(0, 4)).toEqual([401, 401, 401, 401]);
	expect([401, 429]).toContain(wrongStatuses[4]);
	expect(locked.status()).toBe(429);
	expect((await locked.json()).error).toBe('too many failed attempts');
	const retryAfter = Number(locked.headers()['retry-after']);
	expect(retryAfter).toBeGreaterThan(0);
	expect(retryAfter).toBeLessThanOrEqual(15 * 60);
});
