import { describe, expect, it } from 'vitest';
import { ApiError } from '../api/envelope';
import { describeAuthError, describeEventError, formatWait } from './errors';

describe('formatWait', () => {
	it.each([
		[1, '1 detik'],
		[45, '45 detik'],
		[60, '1 menit'],
		[61, '2 menit'],
		[900, '15 menit']
	])('%i seconds reads %j', (seconds, text) => {
		expect(formatWait(seconds)).toBe(text);
	});
});

describe('describeAuthError', () => {
	it('explains an invalid email', () => {
		expect(describeAuthError(new ApiError('invalid email address', 400))).toBe('Alamat email tidak valid.');
	});

	it('explains a wrong or expired code', () => {
		expect(describeAuthError(new ApiError('invalid or expired code', 401))).toBe(
			'Kode salah atau sudah kedaluwarsa.'
		);
	});

	it('tells the host how long to wait after a rate limit', () => {
		const err = new ApiError('too many requests', 429, 90);
		expect(describeAuthError(err)).toBe('Terlalu banyak permintaan. Coba lagi dalam 2 menit.');
	});

	it('distinguishes a lockout after failed attempts', () => {
		const err = new ApiError('too many failed attempts', 429, 900);
		expect(describeAuthError(err)).toBe('Terlalu banyak percobaan gagal. Coba lagi dalam 15 menit.');
	});

	it('still explains a 429 that carries no Retry-After', () => {
		expect(describeAuthError(new ApiError('too many requests', 429))).toBe(
			'Terlalu banyak permintaan. Coba lagi nanti.'
		);
	});

	it('explains a failed email delivery', () => {
		expect(describeAuthError(new ApiError('could not send the code', 502))).toBe(
			'Kode tidak dapat dikirim. Coba lagi sebentar lagi.'
		);
	});

	it('explains a lost connection', () => {
		expect(describeAuthError(new ApiError('Network request failed', 0))).toBe(
			'Tidak dapat terhubung ke server. Periksa koneksi Anda.'
		);
	});

	it.each([new ApiError('internal error', 500), new Error('boom'), 'oops', null])(
		'falls back to a generic message for %j without echoing internals',
		(err) => {
			expect(describeAuthError(err)).toBe('Terjadi kesalahan. Coba lagi.');
		}
	);
});

describe('describeEventError', () => {
	it('shows the server validation message on a 400', () => {
		expect(describeEventError(new ApiError('name is required', 400))).toBe(
			'Data acara ditolak: name is required'
		);
	});

	it('explains a missing event', () => {
		expect(describeEventError(new ApiError('event not found', 404))).toBe('Acara tidak ditemukan.');
	});

	it('explains a lost connection', () => {
		expect(describeEventError(new ApiError('Network request failed', 0))).toBe(
			'Tidak dapat terhubung ke server. Periksa koneksi Anda.'
		);
	});

	it.each([new ApiError('internal error', 500), new Error('boom'), undefined])(
		'falls back to a generic message for %j',
		(err) => {
			expect(describeEventError(err)).toBe('Terjadi kesalahan. Coba lagi.');
		}
	);
});
