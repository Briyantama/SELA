import { ApiError } from '../api/envelope';

const GENERIC = 'Terjadi kesalahan. Coba lagi.';
const OFFLINE = 'Tidak dapat terhubung ke server. Periksa koneksi Anda.';

// The API's fixed message for a lockout after repeated wrong codes (services/auth/http.go).
const LOCKED_MESSAGE = 'too many failed attempts';

/** "45 detik" under a minute, otherwise whole minutes rounded up. */
export function formatWait(seconds: number): string {
	return seconds < 60 ? `${seconds} detik` : `${Math.ceil(seconds / 60)} menit`;
}

function waitSuffix(err: ApiError): string {
	return err.retryAfterSeconds ? `dalam ${formatWait(err.retryAfterSeconds)}` : 'nanti';
}

/**
 * Turns a sign-in failure into text for the host. Only the status and the API's fixed lockout
 * message are read; server text is never shown, so internals cannot leak into the UI.
 */
export function describeAuthError(err: unknown): string {
	if (!(err instanceof ApiError)) return GENERIC;
	switch (err.status) {
		case 0:
			return OFFLINE;
		case 400:
			return 'Alamat email tidak valid.';
		case 401:
			return 'Kode salah atau sudah kedaluwarsa.';
		case 429:
			return err.message === LOCKED_MESSAGE
				? `Terlalu banyak percobaan gagal. Coba lagi ${waitSuffix(err)}.`
				: `Terlalu banyak permintaan. Coba lagi ${waitSuffix(err)}.`;
		case 502:
			return 'Kode tidak dapat dikirim. Coba lagi sebentar lagi.';
		default:
			return GENERIC;
	}
}

/** Turns an event API failure into text for the host. 400 messages are the server's validation text. */
export function describeEventError(err: unknown): string {
	if (!(err instanceof ApiError)) return GENERIC;
	switch (err.status) {
		case 0:
			return OFFLINE;
		case 400:
			return `Data acara ditolak: ${err.message}`;
		case 404:
			return 'Acara tidak ditemukan.';
		default:
			return GENERIC;
	}
}
