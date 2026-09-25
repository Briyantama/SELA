// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./client', () => ({ apiFetch: vi.fn() }));

import { apiFetch } from './client';
import { ApiError } from './envelope';
import {
	beginUpload,
	completeUpload,
	forgetEventId,
	knownEventId,
	listMyMedia,
	resolveShortCode,
	startGuestSession,
	uploadToPresignedUrl
} from './guest';

const fetchMock = vi.mocked(apiFetch);

const EVENT_ID = '7c3f1d64-9a1b-4c2e-8f5a-2b6d1e0c9a77';
const CODE = 'Ab12Cd34';

beforeEach(() => {
	fetchMock.mockReset();
	localStorage.clear();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('resolveShortCode', () => {
	it('reads the public resolver and remembers the event id for the code', async () => {
		// Arrange
		const event = { event_id: EVENT_ID, short_code: CODE, name: 'Pesta', status: 'active' };
		fetchMock.mockResolvedValue(event);

		// Act
		const result = await resolveShortCode(CODE);

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/e/${CODE}`);
		expect(result).toBe(event);
		expect(knownEventId(CODE)).toBe(EVENT_ID);
	});

	it('percent-encodes the code so a hostile value cannot escape the path', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ event_id: EVENT_ID });

		// Act
		await resolveShortCode('../../admin');

		// Assert
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/e/..%2F..%2Fadmin');
	});
});

describe('startGuestSession', () => {
	it('uses the short-code route on first contact and remembers the resolved event id', async () => {
		// The guest cookie is scoped to /api/v1/events/{event_id}, so it never reaches the
		// short-code route: that route can only ever create a session, never resume one.
		// Arrange
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID, resumed: false });

		// Act
		const session = await startGuestSession(CODE);

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/e/${CODE}/guest-session`, {
			method: 'POST',
			body: {}
		});
		expect(session.event_id).toBe(EVENT_ID);
		expect(knownEventId(CODE)).toBe(EVENT_ID);
	});

	it('uses the event-id route once the code is known, so the cookie is sent and the session resumes', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID, resumed: false });
		await startGuestSession(CODE);
		fetchMock.mockReset();
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID, resumed: true });

		// Act
		const session = await startGuestSession(CODE);

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/events/${EVENT_ID}/guest-session`, {
			method: 'POST',
			body: {}
		});
		expect(session.resumed).toBe(true);
	});

	it('takes an event id directly', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID, resumed: true });

		// Act
		await startGuestSession(EVENT_ID);

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/events/${EVENT_ID}/guest-session`, {
			method: 'POST',
			body: {}
		});
	});

	it('sends a nickname when one is given', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID });

		// Act
		await startGuestSession(EVENT_ID, 'Rina');

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/events/${EVENT_ID}/guest-session`, {
			method: 'POST',
			body: { nickname: 'Rina' }
		});
	});

	it('falls back to the short-code route when a remembered event no longer resolves', async () => {
		// A remembered event can be deleted or expire; the guest must still be able to join.
		// Arrange
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID });
		await startGuestSession(CODE);
		fetchMock.mockReset();
		fetchMock.mockRejectedValueOnce(new ApiError('not found', 404));
		fetchMock.mockResolvedValueOnce({ session_id: 's2', event_id: 'other-id' });

		// Act
		const session = await startGuestSession(CODE);

		// Assert
		expect(fetchMock).toHaveBeenNthCalledWith(1, `/api/v1/events/${EVENT_ID}/guest-session`, {
			method: 'POST',
			body: {}
		});
		expect(fetchMock).toHaveBeenNthCalledWith(2, `/api/v1/e/${CODE}/guest-session`, {
			method: 'POST',
			body: {}
		});
		expect(session.event_id).toBe('other-id');
		expect(knownEventId(CODE)).toBe('other-id');
	});

	it('does not retry a failure that is not a missing event', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID });
		await startGuestSession(CODE);
		fetchMock.mockReset();
		fetchMock.mockRejectedValueOnce(new ApiError('too many requests', 429));

		// Act
		const failure = startGuestSession(CODE);

		// Assert
		await expect(failure).rejects.toMatchObject({ status: 429 });
		expect(fetchMock).toHaveBeenCalledTimes(1);
	});

	it('still works when storage is unavailable', async () => {
		// Private browsing can make every localStorage access throw.
		// Arrange
		const boom = () => {
			throw new Error('denied');
		};
		vi.stubGlobal('localStorage', { getItem: boom, setItem: boom, removeItem: boom });
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID });

		// Act
		const session = await startGuestSession(CODE);

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/e/${CODE}/guest-session`, {
			method: 'POST',
			body: {}
		});
		expect(session.event_id).toBe(EVENT_ID);
	});
});

describe('forgetEventId', () => {
	it('drops a remembered mapping', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ session_id: 's1', event_id: EVENT_ID });
		await startGuestSession(CODE);

		// Act
		forgetEventId(CODE);

		// Assert
		expect(knownEventId(CODE)).toBeUndefined();
	});
});

describe('the event-scoped endpoints', () => {
	it('asks for a pre-signed upload', async () => {
		// Arrange
		const upload = { media_id: 'm1', upload: { method: 'PUT', url: 'https://s3/x', headers: {} } };
		fetchMock.mockResolvedValue(upload);

		// Act
		const result = await beginUpload(EVENT_ID, { content_type: 'image/jpeg', size_bytes: 1024 });

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/events/${EVENT_ID}/media/uploads`, {
			method: 'POST',
			body: { content_type: 'image/jpeg', size_bytes: 1024 }
		});
		expect(result).toBe(upload);
	});

	it('completes an upload', async () => {
		// Arrange
		const item = { media_id: 'm1', processing_state: 'ready' };
		fetchMock.mockResolvedValue(item);

		// Act
		const result = await completeUpload(EVENT_ID, 'm1');

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/events/${EVENT_ID}/media/m1/complete`, {
			method: 'POST'
		});
		expect(result).toBe(item);
	});

	it('lists the media of this session and unwraps the items array', async () => {
		// Arrange
		const items = [{ media_id: 'm1' }];
		fetchMock.mockResolvedValue({ items });

		// Act
		const result = await listMyMedia(EVENT_ID);

		// Assert
		expect(fetchMock).toHaveBeenCalledWith(`/api/v1/events/${EVENT_ID}/my-media`);
		expect(result).toBe(items);
	});
});

describe('uploadToPresignedUrl', () => {
	const request = {
		method: 'PUT',
		url: 'https://minio.example.test/sela-media/abc?sig=1',
		headers: { 'Content-Type': 'image/jpeg' },
		expires_at: '2026-12-01T10:00:00Z'
	};

	it('puts the bytes straight at the storage origin, without credentials or the API envelope', async () => {
		// The pre-signed URL is a different origin: sending the session cookie would leak it
		// off-origin, and the response is storage XML, never an API envelope.
		// Arrange
		const send = vi.fn().mockResolvedValue({ ok: true, status: 200 });
		vi.stubGlobal('fetch', send);
		const file = new Blob(['x'], { type: 'image/jpeg' });

		// Act
		await uploadToPresignedUrl(request, file);

		// Assert
		expect(send).toHaveBeenCalledTimes(1);
		const [url, init] = send.mock.calls[0];
		expect(url).toBe(request.url);
		expect(init.method).toBe('PUT');
		expect(init.body).toBe(file);
		expect(init.credentials).toBe('omit');
		expect(new Headers(init.headers).get('Content-Type')).toBe('image/jpeg');
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it('sends every header the API signed, not just the content type', async () => {
		// Dropping a signed header makes S3 reject the PUT with SignatureDoesNotMatch.
		// Arrange
		const send = vi.fn().mockResolvedValue({ ok: true, status: 200 });
		vi.stubGlobal('fetch', send);
		const signed = {
			...request,
			headers: { 'Content-Type': 'image/jpeg', 'x-amz-checksum-crc32': 'abc123' }
		};

		// Act
		await uploadToPresignedUrl(signed, new Blob(['x']));

		// Assert
		const headers = new Headers(send.mock.calls[0][1].headers);
		expect(headers.get('x-amz-checksum-crc32')).toBe('abc123');
	});

	it('reports a rejected upload as an ApiError carrying the status', async () => {
		// Arrange
		vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 403 }));

		// Act
		const failure = uploadToPresignedUrl(request, new Blob(['x']));

		// Assert
		await expect(failure).rejects.toBeInstanceOf(ApiError);
		await expect(failure).rejects.toMatchObject({ status: 403 });
	});

	it('reports a network failure as an ApiError with status 0', async () => {
		// Arrange
		vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('network')));

		// Act
		const failure = uploadToPresignedUrl(request, new Blob(['x']));

		// Assert
		await expect(failure).rejects.toMatchObject({ status: 0 });
	});
});
