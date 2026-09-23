import { describe, expect, it, vi } from 'vitest';
import { clearMailbox, extractOtp, waitForOtp, waitForOtpMessage } from './mailpit';

// The body the API really sends (services/auth/service.go emailBody).
const BODY =
	'Kode masuk Sela Anda: 123456\n\nKode berlaku 5 menit dan hanya bisa dipakai sekali.\n' +
	'Jangan bagikan kode ini kepada siapa pun. Jika Anda tidak memintanya, abaikan email ini.\n';

const EMAIL = 'host@example.test';

function json(body: unknown, status = 200): Response {
	return new Response(JSON.stringify(body), {
		status,
		headers: { 'Content-Type': 'application/json' }
	});
}

const message = (id: string, text = BODY) => ({
	ID: id,
	From: { Name: '', Address: 'no-reply@sela.local' },
	To: [{ Name: '', Address: EMAIL }],
	Subject: 'Kode masuk Sela',
	Text: text
});

/** Answers Mailpit's search with `searches` in turn (the last repeats), and message lookups by id. */
function mailpit(searches: string[][], messages: Record<string, unknown>) {
	let call = 0;
	return vi.fn(async (input: string | URL | Request) => {
		const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url);
		if (url.pathname === '/api/v1/search') {
			const ids = searches[Math.min(call++, searches.length - 1)];
			return json({ messages: ids.map((ID) => ({ ID })), messages_count: ids.length });
		}
		const id = url.pathname.replace('/api/v1/message/', '');
		return id in messages ? json(messages[id]) : json({ error: 'not found' }, 404);
	});
}

describe('extractOtp', () => {
	it('reads the code out of the real email body', () => {
		expect(extractOtp(BODY)).toBe('123456');
	});

	it('ignores digit runs that are not exactly six digits', () => {
		expect(extractOtp('Ref 1234567 and 12345, kode 654321.')).toBe('654321');
	});

	it('accepts the same code repeated', () => {
		expect(extractOtp('Kode 111222. Ulangi: 111222')).toBe('111222');
	});

	it('throws when the text holds no code', () => {
		expect(() => extractOtp('Halo tanpa kode 12345')).toThrow(/6-digit code/);
	});

	it('throws instead of guessing when two different codes appear', () => {
		expect(() => extractOtp('Kode 111111 atau 222222')).toThrow(/more than one/);
	});
});

describe('waitForOtp', () => {
	it('searches by recipient, opens the newest message and returns its code', async () => {
		const fetchImpl = mailpit([['new', 'old']], {
			new: message('new'),
			old: message('old', 'Kode masuk Sela Anda: 999999')
		});

		const code = await waitForOtp(EMAIL, { baseUrl: 'http://mail.test', fetchImpl });

		expect(code).toBe('123456');
		const searched = new URL(String(fetchImpl.mock.calls[0][0]));
		expect(searched.origin).toBe('http://mail.test');
		expect(searched.searchParams.get('query')).toBe(`to:${EMAIL}`);
		expect(String(fetchImpl.mock.calls[1][0])).toBe('http://mail.test/api/v1/message/new');
	});

	it('keeps polling until the email arrives', async () => {
		const fetchImpl = mailpit([[], [], ['m1']], { m1: message('m1') });

		const code = await waitForOtp(EMAIL, { fetchImpl, intervalMs: 1, timeoutMs: 1000 });

		expect(code).toBe('123456');
		expect(fetchImpl).toHaveBeenCalledTimes(4);
	});

	it('times out with the address in the error when nothing arrives', async () => {
		const fetchImpl = mailpit([[]], {});

		await expect(waitForOtp(EMAIL, { fetchImpl, intervalMs: 1, timeoutMs: 30 })).rejects.toThrow(
			`no OTP email for ${EMAIL}`
		);
	});

	it('reports the HTTP status when Mailpit answers with an error', async () => {
		const fetchImpl = vi.fn(async () => json({ error: 'boom' }, 500));

		await expect(waitForOtp(EMAIL, { fetchImpl, intervalMs: 1, timeoutMs: 30 })).rejects.toThrow(/500/);
	});
});

describe('waitForOtpMessage', () => {
	it('also returns sender, subject and text so a test can check the email itself', async () => {
		const fetchImpl = mailpit([['m1']], { m1: message('m1') });

		const mail = await waitForOtpMessage(EMAIL, { fetchImpl });

		expect(mail).toEqual({
			code: '123456',
			from: 'no-reply@sela.local',
			subject: 'Kode masuk Sela',
			text: BODY
		});
	});
});

describe('clearMailbox', () => {
	it('deletes every message', async () => {
		const fetchImpl = vi.fn(async () => json({ status: 'ok' }));

		await clearMailbox({ baseUrl: 'http://mail.test', fetchImpl });

		const [url, init] = fetchImpl.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe('http://mail.test/api/v1/messages');
		expect(init.method).toBe('DELETE');
	});

	it('throws when Mailpit refuses', async () => {
		const fetchImpl = vi.fn(async () => json({ error: 'no' }, 500));

		await expect(clearMailbox({ fetchImpl })).rejects.toThrow(/500/);
	});
});
