/** Error raised when the API reports a failure or returns a malformed body. */
export class ApiError extends Error {
	/**
	 * @param status HTTP status, or 0 when no response was received or the failure is not HTTP.
	 * @param retryAfterSeconds seconds the server asked the caller to wait (Retry-After), if any.
	 */
	constructor(
		message: string,
		readonly status: number = 0,
		readonly retryAfterSeconds?: number
	) {
		super(message);
		this.name = 'ApiError';
	}
}

interface Envelope {
	success: boolean;
	data?: unknown;
	error?: string | null;
}

function isEnvelope(body: unknown): body is Envelope {
	return (
		typeof body === 'object' &&
		body !== null &&
		'success' in body &&
		typeof (body as { success: unknown }).success === 'boolean'
	);
}

/**
 * Unwraps the API response envelope produced by the Go services
 * ({ success, data, error }), returning data or throwing ApiError.
 */
export function unwrapEnvelope<T>(body: unknown): T {
	if (!isEnvelope(body)) {
		throw new ApiError('Unexpected response from server');
	}
	if (!body.success) {
		throw new ApiError(body.error ?? 'Request failed');
	}
	if (!('data' in body)) {
		throw new ApiError('Unexpected response from server');
	}
	return body.data as T;
}
