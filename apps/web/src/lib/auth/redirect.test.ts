import { describe, expect, it } from 'vitest';
import { safeNextPath } from './redirect';

describe('safeNextPath', () => {
	it.each([
		['/events/new', '/events/new'],
		['/events/abc-123', '/events/abc-123'],
		['/events/abc?x=1#y', '/events/abc?x=1#y']
	])('keeps the same-site path %j', (raw, expected) => {
		expect(safeNextPath(raw)).toBe(expected);
	});

	it.each([
		null,
		undefined,
		'',
		'events/new',
		'//evil.example',
		'///evil.example',
		'/\\evil.example',
		'\\\\evil.example',
		'https://evil.example',
		'http://evil.example/events',
		'javascript:alert(1)',
		'data:text/html,x',
		'/events/\nnew',
		'/auth/login',
		'/auth/login?next=/events/new'
	])('falls back to the default for %j', (raw) => {
		expect(safeNextPath(raw)).toBe('/events/new');
	});

	it('uses the caller-supplied fallback', () => {
		expect(safeNextPath('//evil.example', '/')).toBe('/');
	});
});
