import { apiFetch } from './client';

export type RevealMode = 'instant' | 'delayed';

/** A category preset as returned by GET /api/v1/event-categories (FR-01.1). */
export interface Category {
	code: string;
	name: string;
	theme_key: string;
	shot_limit_default: string;
	default_shot_limit: number | null;
	reveal_default: string;
	default_reveal_delay_hours: number | null;
	/** What a new event gets when the host overrides nothing. null = unlimited. */
	effective_shot_limit: number | null;
	effective_reveal_mode: RevealMode;
	effective_reveal_delay_hours: number | null;
}

/** The owner's view of an event. The API never returns the host id or the access token. */
export interface EventView {
	event_id: string;
	category_code: string;
	name: string;
	event_date: string;
	timezone: string;
	status: string;
	short_code: string;
	short_link: string;
	qr_png_url: string;
	qr_svg_url: string;
	shot_limit: number | null;
	reveal_mode: RevealMode;
	reveal_at: string | null;
	package: string;
	created_at: string;
}

/**
 * POST /api/v1/events body (FR-01.2). Overrides are omitted to take the category default;
 * shot_limit 0 means unlimited.
 */
export interface CreateEventBody {
	category_code: string;
	name: string;
	event_date: string;
	shot_limit?: number;
	reveal_mode?: RevealMode;
	reveal_delay_hours?: number;
}

export type QrFormat = 'png' | 'svg';

export async function listCategories(): Promise<Category[]> {
	const data = await apiFetch<{ categories: Category[] }>('/api/v1/event-categories');
	return data.categories;
}

export function createEvent(body: CreateEventBody): Promise<EventView> {
	return apiFetch<EventView>('/api/v1/events', { method: 'POST', body });
}

export function getEvent(id: string): Promise<EventView> {
	return apiFetch<EventView>(`/api/v1/events/${encodeURIComponent(id)}`);
}

/**
 * Same-origin QR image path. The QR endpoints are host-only, so the browser sends the session
 * cookie when it loads this as an image or a download.
 */
export function qrUrl(id: string, format: QrFormat): string {
	return `/api/v1/events/${encodeURIComponent(id)}/qr.${format}`;
}
