import { getProfile, type Profile } from '$lib/api/rbac';

// The host app is a client-side SPA: the HttpOnly session cookie belongs to the browser, so every
// API call is made from the browser rather than during server rendering.
export const ssr = false;

/**
 * Best-effort: a signed-out host (e.g. on /auth/login) or an unreachable API yields no profile,
 * not an error. Each page still enforces its own session requirement independently; this is only
 * what drives the nav and the theme.
 */
export async function load(): Promise<{ profile: Profile | null }> {
	try {
		return { profile: await getProfile() };
	} catch {
		return { profile: null };
	}
}
