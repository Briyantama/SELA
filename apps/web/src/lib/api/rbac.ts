import { apiFetch } from './client';

export type Theme = 'light' | 'dark';

export interface MenuLayout {
	hide_on_mobile: boolean;
	hide_on_tablet: boolean;
	mobile_priority?: number;
}

/** One node of the role's navigation tree, already filtered and translated by the server. */
export interface MenuNode {
	code: string;
	type: 'link' | 'group' | 'divider';
	label: string;
	tooltip?: string;
	icon?: string;
	path?: string;
	sort_order: number;
	layout: MenuLayout;
	children: MenuNode[];
}

export interface Placement {
	desktop: string;
	tablet: string;
	mobile: string;
}

/** One action a role may perform on a resource, with its UI presentation. */
export interface ActionEntry {
	code: string;
	permission: string;
	label: string;
	tooltip?: string;
	confirm_message?: string;
	icon: string;
	variant: 'primary' | 'secondary' | 'neutral' | 'danger';
	destructive: boolean;
	sort_order: number;
	placement: Placement;
}

export interface DropdownItem {
	value: string;
	label: string;
	icon?: string;
	is_default: boolean;
}

export interface Dropdown {
	label: string;
	placeholder?: string;
	searchable: boolean;
	presentation: { mobile: string };
	items: DropdownItem[];
}

export interface Language {
	code: string;
	name: string;
}

export interface Preferences {
	theme: Theme;
	language: string;
	/** Which level each value was resolved from: request | user | role | default. */
	source: { theme: string; language: string };
	supported_languages: Language[];
}

/** The full payload behind GET /api/v1/auth/me. */
export interface Profile {
	active_role: { code: string; name: string };
	preferences: Preferences;
	menus: MenuNode[];
	permissions: string[];
	actions: Record<string, ActionEntry[]>;
	dropdowns: Record<string, Dropdown>;
}

/** PATCH /api/v1/me/preferences body. Omitting a field leaves that preference unchanged. */
export interface PreferencesInput {
	theme?: Theme;
	language?: string;
}

export function getProfile(lang?: string): Promise<Profile> {
	const path = lang ? `/api/v1/auth/me?lang=${encodeURIComponent(lang)}` : '/api/v1/auth/me';
	return apiFetch<Profile>(path);
}

export function updatePreferences(input: PreferencesInput): Promise<Profile> {
	return apiFetch<Profile>('/api/v1/me/preferences', { method: 'PATCH', body: input });
}
