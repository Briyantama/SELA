import { apiFetch } from './client';

interface RequestOtpData {
	expires_in_seconds: number;
}

interface VerifyOtpData {
	host_id: string;
	is_new_host: boolean;
	expires_in_seconds: number;
}

/** Asks the API to email a one-time code (FR-09.1). */
export async function requestOtp(email: string): Promise<{ expiresInSeconds: number }> {
	const data = await apiFetch<RequestOtpData>('/api/v1/auth/otp/request', {
		method: 'POST',
		body: { email }
	});
	return { expiresInSeconds: data.expires_in_seconds };
}

/**
 * Exchanges the emailed code for a session. The API sets the HttpOnly session cookie; the token
 * is never visible to this code.
 */
export async function verifyOtp(
	email: string,
	code: string
): Promise<{ hostId: string; isNewHost: boolean }> {
	const data = await apiFetch<VerifyOtpData>('/api/v1/auth/otp/verify', {
		method: 'POST',
		body: { email, code }
	});
	return { hostId: data.host_id, isNewHost: data.is_new_host };
}
