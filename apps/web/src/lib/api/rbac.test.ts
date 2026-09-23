import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./client', () => ({ apiFetch: vi.fn() }));

import { apiFetch } from './client';
import { getProfile, updatePreferences } from './rbac';

const fetchMock = vi.mocked(apiFetch);

beforeEach(() => {
	fetchMock.mockReset();
});

const sampleProfile = {
	active_role: { code: 'host', name: 'Host' },
	preferences: {
		theme: 'light',
		language: 'id',
		source: { theme: 'default', language: 'default' },
		supported_languages: [
			{ code: 'id', name: 'Bahasa Indonesia' },
			{ code: 'en', name: 'English' }
		]
	},
	menus: [
		{
			code: 'events_new',
			type: 'link',
			label: 'Buat Acara Baru',
			sort_order: 10,
			path: '/events/new',
			layout: { hide_on_mobile: false, hide_on_tablet: false, mobile_priority: 1 },
			children: []
		}
	],
	permissions: ['events:create', 'profile:update'],
	actions: {},
	dropdowns: {}
};

describe('getProfile', () => {
	it('gets the profile with no query string when no language is given', async () => {
		fetchMock.mockResolvedValue(sampleProfile);

		const result = await getProfile();

		expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/me');
		expect(result).toBe(sampleProfile);
	});

	it('passes the language override as a query parameter', async () => {
		fetchMock.mockResolvedValue(sampleProfile);

		await getProfile('en');

		expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/me?lang=en');
	});
});

describe('updatePreferences', () => {
	it('PATCHes only the fields given', async () => {
		fetchMock.mockResolvedValue(sampleProfile);

		const result = await updatePreferences({ theme: 'dark' });

		expect(fetchMock).toHaveBeenCalledWith('/api/v1/me/preferences', {
			method: 'PATCH',
			body: { theme: 'dark' }
		});
		expect(result).toBe(sampleProfile);
	});

	it('sends both fields when both are given', async () => {
		fetchMock.mockResolvedValue(sampleProfile);

		await updatePreferences({ theme: 'light', language: 'en' });

		expect(fetchMock).toHaveBeenCalledWith('/api/v1/me/preferences', {
			method: 'PATCH',
			body: { theme: 'light', language: 'en' }
		});
	});
});
