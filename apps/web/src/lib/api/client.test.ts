import { afterEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from './envelope';
import { apiFetch } from './client';

function respond(status: number, body: unknown, headers: Record<string, string> = {}): Response {
	const text = typeof body === 'string' ? body : JSON.stringify(body);
	return new Response(text, { status, headers: { 'Content-Type': 'application/json', ...headers } });
}

function stubFetch(response: Response | Error) {
	const fn = vi.fn(async () => {
		if (response instanceof Error) throw response;
		return response;
	});
	vi.stubGlobal('fetch', fn);
	return fn;
}

afterEach(() => {
	vi.unstubAllGlobals();
	vi.useRealTimers();
});

describe('apiFetch', () => {
	it('sends credentials and returns the unwrapped data of a success envelope', async () => {
		// Arrange
		const fetchMock = stubFetch(respond(200, { success: true, data: { event_id: 'e1' }, error: null }));

		// Act
		const data = await apiFetch<{ event_id: string }>('/api/v1/events/e1');

		// Assert
		expect(data).toEqual({ event_id: 'e1' });
		const [path, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
		expect(path).toBe('/api/v1/events/e1');
		expect(init.credentials).toBe('include');
		expect(init.method).toBe('GET');
		expect(init.body).toBeUndefined();
	});

	it('sends a JSON body with a content type on POST', async () => {
		// Arrange
		const fetchMock = stubFetch(respond(200, { success: true, data: {}, error: null }));

		// Act
		await apiFetch('/api/v1/events', { method: 'POST', body: { name: 'Nikah' } });

		// Assert
		const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
		expect(init.method).toBe('POST');
		expect(init.body).toBe('{"name":"Nikah"}');
		expect(new Headers(init.headers).get('Content-Type')).toBe('application/json');
	});

	it('throws an ApiError carrying the status and server message on a failure envelope', async () => {
		// Arrange
		stubFetch(respond(400, { success: false, data: null, error: 'name is required' }));

		// Act
		const err = await apiFetch('/x').catch((e: unknown) => e);

		// Assert
		expect(err).toBeInstanceOf(ApiError);
		expect((err as ApiError).message).toBe('name is required');
		expect((err as ApiError).status).toBe(400);
		expect((err as ApiError).retryAfterSeconds).toBeUndefined();
	});

	it('exposes Retry-After seconds on a 429', async () => {
		// Arrange
		stubFetch(respond(429, { success: false, data: null, error: 'too many requests' }, { 'Retry-After': '42' }));

		// Act
		const err = (await apiFetch('/x').catch((e: unknown) => e)) as ApiError;

		// Assert
		expect(err.status).toBe(429);
		expect(err.retryAfterSeconds).toBe(42);
	});

	it.each(['soon', '-5', '0', ''])('ignores an unusable Retry-After of %j', async (value) => {
		// Arrange
		stubFetch(respond(429, { success: false, data: null, error: 'x' }, { 'Retry-After': value }));

		// Act
		const err = (await apiFetch('/x').catch((e: unknown) => e)) as ApiError;

		// Assert
		expect(err.retryAfterSeconds).toBeUndefined();
	});

	it('throws an ApiError with the HTTP status when the body is not JSON', async () => {
		// Arrange
		stubFetch(new Response('<html>bad gateway</html>', { status: 502 }));

		// Act
		const err = (await apiFetch('/x').catch((e: unknown) => e)) as ApiError;

		// Assert
		expect(err).toBeInstanceOf(ApiError);
		expect(err.status).toBe(502);
	});

	it('gives fetch an abort signal so a request can be cancelled', async () => {
		// Arrange
		const fetchMock = stubFetch(respond(200, { success: true, data: {}, error: null }));

		// Act
		await apiFetch('/x');

		// Assert
		const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
		expect(init.signal).toBeInstanceOf(AbortSignal);
	});

	it('aborts a request the server never answers and reports status 0', async () => {
		// Arrange
		vi.useFakeTimers();
		vi.stubGlobal(
			'fetch',
			vi.fn(
				(_path: string, init: RequestInit) =>
					new Promise((_resolve, reject) => {
						init.signal?.addEventListener('abort', () =>
							reject(new DOMException('aborted', 'AbortError'))
						);
					})
			)
		);

		// Act
		const pending = apiFetch('/x', { timeoutMs: 1000 }).catch((e: unknown) => e);
		await vi.advanceTimersByTimeAsync(1000);
		const err = (await pending) as ApiError;

		// Assert
		expect(err).toBeInstanceOf(ApiError);
		expect(err.status).toBe(0);
	});

	it('throws an ApiError with status 0 when the network fails', async () => {
		// Arrange
		stubFetch(new TypeError('Failed to fetch'));

		// Act
		const err = (await apiFetch('/x').catch((e: unknown) => e)) as ApiError;

		// Assert
		expect(err).toBeInstanceOf(ApiError);
		expect(err.status).toBe(0);
	});
});
