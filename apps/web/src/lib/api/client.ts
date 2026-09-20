import { ApiError, unwrapEnvelope } from './envelope';

export interface ApiRequest {
	method?: 'GET' | 'POST';
	/** Serialised as JSON. */
	body?: unknown;
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
	const init: RequestInit = {
		method: request.method ?? 'GET',
		credentials: 'include',
		headers
	};
	if (request.body !== undefined) {
		headers.set('Content-Type', 'application/json');
		init.body = JSON.stringify(request.body);
	}

	let response: Response;
	try {
		response = await fetch(path, init);
	} catch {
		throw new ApiError('Network request failed', 0);
	}

	let parsed: unknown;
	try {
		parsed = await response.json();
	} catch {
		throw new ApiError('Unexpected response from server', response.status);
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
