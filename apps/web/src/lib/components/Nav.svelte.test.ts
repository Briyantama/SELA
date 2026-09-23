// @vitest-environment jsdom
import { render, screen } from '@testing-library/svelte';
import { describe, expect, it, vi } from 'vitest';
import type { MenuNode } from '$lib/api/rbac';

const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/events/new') }));
vi.mock('$app/state', () => ({ page: pageState }));

import Nav from './Nav.svelte';

const menus: MenuNode[] = [
	{
		code: 'events_new', type: 'link', label: 'Buat Acara Baru', sort_order: 10, path: '/events/new',
		layout: { hide_on_mobile: false, hide_on_tablet: false, mobile_priority: 1 }, children: []
	},
	{
		code: 'settings', type: 'link', label: 'Pengaturan', sort_order: 20, path: '/settings',
		layout: { hide_on_mobile: false, hide_on_tablet: false, mobile_priority: 2 }, children: []
	}
];

describe('Nav', () => {
	it('renders a link for every top-level menu, in order', () => {
		render(Nav, { menus });

		const links = screen.getAllByRole('link');
		expect(links.map((l) => l.textContent)).toEqual(['Buat Acara Baru', 'Pengaturan']);
		expect(links[0]).toHaveAttribute('href', '/events/new');
		expect(links[1]).toHaveAttribute('href', '/settings');
	});

	it('marks the current route as the active link', () => {
		render(Nav, { menus });

		expect(screen.getByRole('link', { name: 'Buat Acara Baru' })).toHaveAttribute('aria-current', 'page');
		expect(screen.getByRole('link', { name: 'Pengaturan' })).not.toHaveAttribute('aria-current');
	});

	it('skips a menu with no path (a group or divider)', () => {
		const withGroup: MenuNode[] = [
			{
				code: 'grp', type: 'group', label: 'Grup', sort_order: 5,
				layout: { hide_on_mobile: false, hide_on_tablet: false }, children: []
			},
			...menus
		];

		render(Nav, { menus: withGroup });

		expect(screen.queryByText('Grup')).not.toBeInTheDocument();
		expect(screen.getAllByRole('link')).toHaveLength(2);
	});

	it('renders nothing when there are no menus', () => {
		render(Nav, { menus: [] });

		expect(screen.queryByRole('link')).not.toBeInTheDocument();
	});
});
