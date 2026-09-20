import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./client', () => ({ apiFetch: vi.fn() }));

import { apiFetch } from './client';
import { createEvent, getEvent, listCategories, qrUrl } from './events';

const fetchMock = vi.mocked(apiFetch);

beforeEach(() => fetchMock.mockReset());

describe('listCategories', () => {
	it('gets the public category list and unwraps the categories array', async () => {
		// Arrange
		const categories = [{ code: 'wedding', name: 'Pernikahan' }];
		fetchMock.mockResolvedValue({ categories });

		// Act
		const result = await listCategories();

		// Assert
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/event-categories');
		expect(result).toBe(categories);
	});
});

describe('createEvent', () => {
	it('posts the body to the events endpoint and returns the created event', async () => {
		// Arrange
		const created = { event_id: 'e1' };
		fetchMock.mockResolvedValue(created);
		const body = { category_code: 'wedding', name: 'Nikah A & B', event_date: '2026-12-05' };

		// Act
		const result = await createEvent(body);

		// Assert
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/events', { method: 'POST', body });
		expect(result).toBe(created);
	});
});

describe('getEvent', () => {
	it('gets the event by id and percent-encodes the id', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ event_id: 'e1' });

		// Act
		await getEvent('a/b c');

		// Assert
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/events/a%2Fb%20c');
	});
});

describe('qrUrl', () => {
	it.each(['png', 'svg'] as const)('builds the same-origin %s QR path', (format) => {
		expect(qrUrl('e1', format)).toBe(`/api/v1/events/e1/qr.${format}`);
	});

	it('percent-encodes the id', () => {
		expect(qrUrl('../x', 'png')).toBe('/api/v1/events/..%2Fx/qr.png');
	});
});
