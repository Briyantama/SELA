import { ApiError, unwrapEnvelope } from './envelope';

const DEFAULT_TIMEOUT_MS = 15_000;

export interface ApiRequest {
	method?: 'GET' | 'POST' | 'PATCH';
	/** Serialised as JSON. */
	body?: unknown;
	/** The request is aborted, and fails with status 0, if the response is not complete by then. */
	timeoutMs?: number;
}

/** Only a positive whole number of seconds is a usable Retry-After. */
function parseRetryAfter(value: string | null): number | undefined {
	if (value === null || !/^\d+$/.test(value)) return undefined;
	const seconds = Number(value);
	return seconds > 0 ? seconds : undefined;
}

/**
 * Calls the SELA API and returns the unwrapped `data` of the response envelope.
 *
 * Requests carry the HttpOnly session cookie (`credentials: 'include'`); paths are relative so the
 * browser reaches the API through the web app's own origin. Every failure, including network and
 * malformed-body failures, is an ApiError with the HTTP status (0 when there was no response).
 */
export async function apiFetch<T>(path: string, request: ApiRequest = {}): Promise<T> {
	const headers = new Headers({ Accept: 'application/json' });
	const controller = new AbortController();
	const init: RequestInit = {
		method: request.method ?? 'GET',
		credentials: 'include',
		headers,
		signal: controller.signal
	};
	if (request.body !== undefined) {
		headers.set('Content-Type', 'application/json');
		init.body = JSON.stringify(request.body);
	}

	// A stalled mobile connection must not leave a form disabled forever.
	const timer = setTimeout(() => controller.abort(), request.timeoutMs ?? DEFAULT_TIMEOUT_MS);
	let response: Response;
	let parsed: unknown;
	try {
		try {
			response = await fetch(path, init);
		} catch {
			throw new ApiError('Network request failed', 0);
		}
		try {
			parsed = await response.json();
		} catch {
			throw new ApiError(
				controller.signal.aborted ? 'Network request failed' : 'Unexpected response from server',
				controller.signal.aborted ? 0 : response.status
			);
		}
	} finally {
		clearTimeout(timer);
	}

	try {
		return unwrapEnvelope<T>(parsed);
	} catch (err) {
		if (!(err instanceof ApiError)) throw err;
		throw new ApiError(
			err.message,
			response.status,
			parseRetryAfter(response.headers.get('Retry-After'))
		);
	}
}
