import { apiFetch } from './client';
import { ApiError } from './envelope';
import type { RevealMode } from './events';

/**
 * What a guest may learn from a short link (FSD 6). It carries no host id, access token, package or
 * created_at by construction: the API never sends them on this route.
 */
export interface PublicEvent {
	event_id: string;
	short_code: string;
	name: string;
	event_date: string;
	timezone: string;
	category_code: string;
	theme_key: string;
	status: string;
	shot_limit: number | null;
	reveal_mode: RevealMode;
	reveal_at: string | null;
}

/** The anonymous session behind the `sela_guest` cookie. The token itself never reaches JavaScript. */
export interface GuestSession {
	session_id: string;
	event_id: string;
	resumed: boolean;
	shot_limit: number | null;
	shots_remaining: number | null;
	involves_minors: boolean;
	expires_in_seconds: number;
}

/** The pre-signed request the API signed for one upload. Every header is part of the signature. */
export interface PresignedRequest {
	method: string;
	url: string;
	headers: Record<string, string>;
	expires_at: string;
}

export interface BeginUploadResult {
	media_id: string;
	upload: PresignedRequest;
	shots_remaining: number | null;
}

/** One item in a gallery. It never carries the storage key or another guest session id. */
export interface MediaItem {
	media_id: string;
	kind: string;
	content_type: string;
	processing_state: string;
	status: string;
	size_bytes: number | null;
	hall_of_fame_rank: number | null;
	uploaded_at: string;
	ready_at: string | null;
	/** Short-lived pre-signed URL; absent until the item is ready (FR-SEC.1). */
	url?: string;
}

export interface UploadParams {
	content_type: string;
	size_bytes: number;
}

const STORAGE_PREFIX = 'sela.guest.event.';

const EVENT_ID_PATTERN =
	/^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

/** Short codes and event ids arrive through the same argument; only ids are UUIDs. */
function isEventId(value: string): boolean {
	return EVENT_ID_PATTERN.test(value);
}

/**
 * The event id last seen for a short code, or undefined.
 *
 * This is not a cache for its own sake. The `sela_guest` cookie is scoped to
 * `/api/v1/events/{event_id}`, so the browser never sends it to a short-code URL and that route
 * can only ever create a session. Remembering the id lets a returning guest call the event-id
 * route, where the cookie *is* sent and the session resumes instead of a second one being made.
 */
export function knownEventId(shortCode: string): string | undefined {
	try {
		return localStorage.getItem(STORAGE_PREFIX + shortCode) ?? undefined;
	} catch {
		// Private browsing and blocked site data make this throw. A guest without storage simply
		// opens a fresh session on every load, which still works.
		return undefined;
	}
}

function rememberEventId(shortCode: string, eventId: string): void {
	try {
		localStorage.setItem(STORAGE_PREFIX + shortCode, eventId);
	} catch {
		// Losing the mapping costs a resumed session, never correctness.
	}
}

/** Drops a remembered mapping, so the next visit resolves the code again. */
export function forgetEventId(shortCode: string): void {
	try {
		localStorage.removeItem(STORAGE_PREFIX + shortCode);
	} catch {
		// Nothing to undo.
	}
}

/**
 * Reads the public short-link resolver (FR-01.3). Every unusable code -- unknown, malformed, not
 * active, expired -- answers with the same 404, so a failure says nothing about which it was.
 */
export async function resolveShortCode(shortCode: string): Promise<PublicEvent> {
	const event = await apiFetch<PublicEvent>(`/api/v1/e/${encodeURIComponent(shortCode)}`);
	if (event.event_id) rememberEventId(shortCode, event.event_id);
	return event;
}

function sessionBody(nickname?: string): Record<string, string> {
	return nickname ? { nickname } : {};
}

function openByEventId(eventId: string, nickname?: string): Promise<GuestSession> {
	return apiFetch<GuestSession>(`/api/v1/events/${encodeURIComponent(eventId)}/guest-session`, {
		method: 'POST',
		body: sessionBody(nickname)
	});
}

async function openByShortCode(shortCode: string, nickname?: string): Promise<GuestSession> {
	const session = await apiFetch<GuestSession>(
		`/api/v1/e/${encodeURIComponent(shortCode)}/guest-session`,
		{ method: 'POST', body: sessionBody(nickname) }
	);
	rememberEventId(shortCode, session.event_id);
	return session;
}

/**
 * Opens (or resumes) the anonymous session for an event, given either its short code or its id.
 *
 * A code whose event is already known goes to the event-id route, because that is the only route
 * the guest cookie reaches -- so a reload resumes rather than spending another session against the
 * per-address limit. A code seen for the first time goes to the short-code route, which resolves
 * and joins in one round trip so the camera can start sooner.
 */
export async function startGuestSession(
	shortCodeOrId: string,
	nickname?: string
): Promise<GuestSession> {
	if (isEventId(shortCodeOrId)) return openByEventId(shortCodeOrId, nickname);

	const remembered = knownEventId(shortCodeOrId);
	if (remembered !== undefined) {
		try {
			return await openByEventId(remembered, nickname);
		} catch (error) {
			// The remembered event can have been deleted or expired since. Any other failure --
			// rate limiting, offline, a server fault -- is the caller's to handle, not ours to retry.
			if (!(error instanceof ApiError) || error.status !== 404) throw error;
			forgetEventId(shortCodeOrId);
		}
	}
	return openByShortCode(shortCodeOrId, nickname);
}

/** Asks for a pre-signed upload for one file. The quota is spent here, not on completion. */
export function beginUpload(eventId: string, params: UploadParams): Promise<BeginUploadResult> {
	return apiFetch<BeginUploadResult>(
		`/api/v1/events/${encodeURIComponent(eventId)}/media/uploads`,
		{ method: 'POST', body: params }
	);
}

/** Tells the API the bytes are in place, so it can validate, strip metadata and publish them. */
export function completeUpload(eventId: string, mediaId: string): Promise<MediaItem> {
	return apiFetch<MediaItem>(
		`/api/v1/events/${encodeURIComponent(eventId)}/media/${encodeURIComponent(mediaId)}/complete`,
		{ method: 'POST' }
	);
}

/** The gallery of this guest session only; the backend scopes it in SQL (FR-05.5). */
export async function listMyMedia(eventId: string): Promise<MediaItem[]> {
	const data = await apiFetch<{ items: MediaItem[] }>(
		`/api/v1/events/${encodeURIComponent(eventId)}/my-media`
	);
	return data.items;
}

/**
 * Sends the bytes to the pre-signed URL.
 *
 * This deliberately does not go through apiFetch: the URL is object storage on another origin, so
 * there is no session cookie to send (`credentials: 'omit'` keeps it off-origin), no response
 * envelope to unwrap, and the signed headers must be replayed exactly or the signature check fails.
 * Failures are still reported as ApiError so callers handle one error type.
 */
export async function uploadToPresignedUrl(request: PresignedRequest, file: Blob): Promise<void> {
	let response: Response;
	try {
		response = await fetch(request.url, {
			method: request.method,
			body: file,
			headers: request.headers,
			credentials: 'omit',
			mode: 'cors'
		});
	} catch {
		throw new ApiError('Upload failed. Check your connection.', 0);
	}
	if (!response.ok) {
		throw new ApiError('Upload was rejected by storage.', response.status);
	}
}
