import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('./client', () => ({ apiFetch: vi.fn() }));

import { apiFetch } from './client';
import { requestOtp, verifyOtp } from './auth';

const fetchMock = vi.mocked(apiFetch);

beforeEach(() => fetchMock.mockReset());

describe('requestOtp', () => {
	it('posts the email to the OTP request endpoint and returns the expiry', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ expires_in_seconds: 300 });

		// Act
		const result = await requestOtp('host@example.test');

		// Assert
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/otp/request', {
			method: 'POST',
			body: { email: 'host@example.test' }
		});
		expect(result).toEqual({ expiresInSeconds: 300 });
	});
});

describe('verifyOtp', () => {
	it('posts the email and code to the verify endpoint and returns the host', async () => {
		// Arrange
		fetchMock.mockResolvedValue({ host_id: 'h1', is_new_host: true, expires_in_seconds: 3600 });

		// Act
		const result = await verifyOtp('host@example.test', '123456');

		// Assert
		expect(fetchMock).toHaveBeenCalledWith('/api/v1/auth/otp/verify', {
			method: 'POST',
			body: { email: 'host@example.test', code: '123456' }
		});
		expect(result).toEqual({ hostId: 'h1', isNewHost: true });
	});

	it('propagates the client error', async () => {
		// Arrange
		fetchMock.mockRejectedValue(new Error('boom'));

		// Act + Assert
		await expect(verifyOtp('a@b.test', '000000')).rejects.toThrow('boom');
	});
});
