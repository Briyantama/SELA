// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '$lib/api/envelope';
import type { Profile } from '$lib/api/rbac';

const nav = vi.hoisted(() => ({ invalidateAll: vi.fn() }));
const rbacApi = vi.hoisted(() => ({ getProfile: vi.fn(), updatePreferences: vi.fn() }));

vi.mock('$app/navigation', () => nav);
vi.mock('$lib/api/rbac', () => rbacApi);

import SettingsPage from './+page.svelte';

function profile(overrides: Partial<Profile['preferences']> = {}): Profile {
	return {
		active_role: { code: 'host', name: 'Host' },
		preferences: {
			theme: 'light',
			language: 'id',
			source: { theme: 'default', language: 'default' },
			supported_languages: [
				{ code: 'id', name: 'Bahasa Indonesia' },
				{ code: 'en', name: 'English' }
			],
			...overrides
		},
		menus: [],
		permissions: [],
		actions: {},
		dropdowns: {}
	};
}

beforeEach(() => {
	rbacApi.getProfile.mockReset().mockResolvedValue(profile());
	rbacApi.updatePreferences.mockReset();
	nav.invalidateAll.mockReset();
	document.documentElement.removeAttribute('data-theme');
});

describe('settings page', () => {
	it('loads and shows the role and current preferences', async () => {
		render(SettingsPage);

		expect(await screen.findByRole('heading', { level: 1, name: 'Pengaturan' })).toBeInTheDocument();
		expect(screen.getByText(/Host/)).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Terang' })).toHaveAttribute('aria-pressed', 'true');
		expect(screen.getByRole('button', { name: 'Gelap' })).toHaveAttribute('aria-pressed', 'false');
	});

	it('switches to dark, applies it to the document and refreshes the layout data', async () => {
		rbacApi.updatePreferences.mockResolvedValue(profile({ theme: 'dark' }));
		render(SettingsPage);
		await screen.findByRole('heading', { level: 1 });

		await fireEvent.click(screen.getByRole('button', { name: 'Gelap' }));

		await waitFor(() => expect(rbacApi.updatePreferences).toHaveBeenCalledWith({ theme: 'dark' }));
		await waitFor(() => expect(document.documentElement.dataset.theme).toBe('dark'));
		expect(nav.invalidateAll).toHaveBeenCalled();
	});

	it('does nothing when the already-active theme is clicked again', async () => {
		render(SettingsPage);
		await screen.findByRole('heading', { level: 1 });

		await fireEvent.click(screen.getByRole('button', { name: 'Terang' }));

		expect(rbacApi.updatePreferences).not.toHaveBeenCalled();
	});

	it('switches language, and the page relabels itself in the new language', async () => {
		rbacApi.updatePreferences.mockResolvedValue(profile({ language: 'en' }));
		render(SettingsPage);
		await screen.findByRole('heading', { level: 1 });

		await fireEvent.click(screen.getByRole('button', { name: 'English' }));

		expect(rbacApi.updatePreferences).toHaveBeenCalledWith({ language: 'en' });
		expect(await screen.findByRole('heading', { level: 1, name: 'Settings' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Light' })).toBeInTheDocument();
	});

	it('shows an alert if the initial load fails', async () => {
		rbacApi.getProfile.mockRejectedValue(new ApiError('Network request failed', 0));
		render(SettingsPage);

		expect(await screen.findByRole('alert')).toBeInTheDocument();
	});

	it('shows an alert if saving a preference fails, without losing the current values', async () => {
		rbacApi.updatePreferences.mockRejectedValue(new ApiError('unknown language code', 400));
		render(SettingsPage);
		await screen.findByRole('heading', { level: 1 });

		await fireEvent.click(screen.getByRole('button', { name: 'English' }));

		expect(await screen.findByRole('alert')).toHaveTextContent('unknown language code');
		expect(screen.getByRole('button', { name: 'Terang' })).toHaveAttribute('aria-pressed', 'true');
	});
});
