// Reads the OTP email the API sends through Mailpit (the dev mail sink from deploy/docker-compose.yml)
// using its REST API, so end-to-end tests can sign in exactly as a host would.

export interface MailpitOptions {
	/** Defaults to E2E_MAILPIT_URL, then http://127.0.0.1:8025. */
	baseUrl?: string;
	timeoutMs?: number;
	intervalMs?: number;
	fetchImpl?: typeof fetch;
}

export interface OtpMail {
	code: string;
	from: string;
	subject: string;
	text: string;
}

interface MailpitMessage {
	ID: string;
	From: { Address: string };
	Subject: string;
	Text: string;
}

const DEFAULT_TIMEOUT_MS = 15_000;
const DEFAULT_INTERVAL_MS = 250;

// Exactly six digits: neither neighbour may be a digit, so a longer number is never sliced.
const SIX_DIGITS = /(?<!\d)\d{6}(?!\d)/g;

const resolveBaseUrl = (options: MailpitOptions): string =>
	(options.baseUrl ?? process.env.E2E_MAILPIT_URL ?? 'http://127.0.0.1:8025').replace(/\/$/, '');

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

/** Returns the one 6-digit code in the text; throws rather than guess when there is none or several. */
export function extractOtp(text: string): string {
	const codes = new Set(text.match(SIX_DIGITS) ?? []);
	if (codes.size === 0) throw new Error('no 6-digit code found in the email text');
	if (codes.size > 1) throw new Error('more than one 6-digit code found in the email text');
	return [...codes][0];
}

async function mailpitJson<T>(
	fetchImpl: typeof fetch,
	url: string,
	init?: RequestInit
): Promise<T> {
	const response = await (init ? fetchImpl(url, init) : fetchImpl(url));
	if (!response.ok) {
		throw new Error(`Mailpit ${init?.method ?? 'GET'} ${new URL(url).pathname} failed: HTTP ${response.status}`);
	}
	return (await response.json()) as T;
}

/**
 * Waits for the newest email addressed to `email` and returns it with its OTP.
 * Mailpit lists newest first, so the first search hit is the latest code the API sent.
 */
export async function waitForOtpMessage(email: string, options: MailpitOptions = {}): Promise<OtpMail> {
	const baseUrl = resolveBaseUrl(options);
	const fetchImpl = options.fetchImpl ?? fetch;
	const timeoutMs = options.timeoutMs ?? DEFAULT_TIMEOUT_MS;
	const intervalMs = options.intervalMs ?? DEFAULT_INTERVAL_MS;
	const searchUrl = `${baseUrl}/api/v1/search?${new URLSearchParams({ query: `to:${email}` })}`;

	const deadline = Date.now() + timeoutMs;
	for (;;) {
		const found = await mailpitJson<{ messages: { ID: string }[] }>(fetchImpl, searchUrl);
		if (found.messages.length > 0) {
			const id = encodeURIComponent(found.messages[0].ID);
			const mail = await mailpitJson<MailpitMessage>(fetchImpl, `${baseUrl}/api/v1/message/${id}`);
			return {
				code: extractOtp(mail.Text),
				from: mail.From.Address,
				subject: mail.Subject,
				text: mail.Text
			};
		}
		if (Date.now() >= deadline) {
			throw new Error(`no OTP email for ${email} within ${timeoutMs}ms`);
		}
		await sleep(intervalMs);
	}
}

/** Waits for the newest email addressed to `email` and returns just its OTP. */
export async function waitForOtp(email: string, options: MailpitOptions = {}): Promise<string> {
	return (await waitForOtpMessage(email, options)).code;
}

/** Deletes every message in the mailbox. */
export async function clearMailbox(options: MailpitOptions = {}): Promise<void> {
	const fetchImpl = options.fetchImpl ?? fetch;
	await mailpitJson(fetchImpl, `${resolveBaseUrl(options)}/api/v1/messages`, { method: 'DELETE' });
}
