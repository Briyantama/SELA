import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('$lib/api/rbac', () => ({ getProfile: vi.fn() }));

import { getProfile, type Profile } from '$lib/api/rbac';
import { load } from './+layout';

const fetchMock = vi.mocked(getProfile);

beforeEach(() => {
	fetchMock.mockReset();
});

const sampleProfile = {
	active_role: { code: 'host', name: 'Host' },
	preferences: {
		theme: 'light',
		language: 'id',
		source: { theme: 'default', language: 'default' },
		supported_languages: []
	},
	menus: [],
	permissions: [],
	actions: {},
	dropdowns: {}
} satisfies Profile;

describe('root layout load', () => {
	it('returns the profile when the fetch succeeds', async () => {
		fetchMock.mockResolvedValue(sampleProfile);

		const result = await load();

		expect(result).toEqual({ profile: sampleProfile });
	});

	it('returns a null profile when the fetch fails (e.g. the host is signed out)', async () => {
		fetchMock.mockRejectedValue(new Error('401'));

		const result = await load();

		expect(result).toEqual({ profile: null });
	});
});
