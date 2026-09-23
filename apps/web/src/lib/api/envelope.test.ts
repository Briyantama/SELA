import { describe, expect, it } from 'vitest';
import { ApiError, unwrapEnvelope } from './envelope';

describe('unwrapEnvelope', () => {
	it('returns data from a success envelope', () => {
		// Arrange
		const body = { success: true, data: { event_id: 'abc' }, error: null };

		// Act
		const data = unwrapEnvelope<{ event_id: string }>(body);

		// Assert
		expect(data).toEqual({ event_id: 'abc' });
	});

	it('throws ApiError with the server message on a failure envelope', () => {
		// Arrange
		const body = { success: false, data: null, error: 'name is required' };

		// Act + Assert
		expect(() => unwrapEnvelope(body)).toThrowError(ApiError);
		expect(() => unwrapEnvelope(body)).toThrowError('name is required');
	});

	it('throws ApiError when the body is not an envelope', () => {
		expect(() => unwrapEnvelope({ hello: 'world' })).toThrowError(ApiError);
		expect(() => unwrapEnvelope(null)).toThrowError(ApiError);
		expect(() => unwrapEnvelope('oops')).toThrowError(ApiError);
	});

	it('throws ApiError when success is true but the envelope has no data field', () => {
		expect(() => unwrapEnvelope({ success: true, error: null })).toThrowError(ApiError);
	});
});
