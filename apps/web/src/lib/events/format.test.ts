import { describe, expect, it } from 'vitest';
import { describePolicy, formatEventDate } from './format';

describe('formatEventDate', () => {
	it('writes a calendar date in Indonesian regardless of the runtime time zone', () => {
		expect(formatEventDate('2026-12-05')).toBe('Sabtu, 5 Desember 2026');
	});

	it('returns the input unchanged when it is not a date', () => {
		expect(formatEventDate('besok')).toBe('besok');
	});
});

describe('describePolicy', () => {
	it('describes unlimited shots with an instant reveal', () => {
		expect(describePolicy({ shot_limit: null, reveal_mode: 'instant', reveal_at: null })).toBe(
			'Jepretan tak terbatas · Reveal langsung'
		);
	});

	it('describes a per-guest limit', () => {
		expect(describePolicy({ shot_limit: 30, reveal_mode: 'instant', reveal_at: null })).toBe(
			'Jepretan 30 per tamu · Reveal langsung'
		);
	});

	it('shows the delayed reveal moment in WIB', () => {
		const text = describePolicy({
			shot_limit: null,
			reveal_mode: 'delayed',
			reveal_at: '2026-12-07T05:00:00Z'
		});

		expect(text).toContain('Reveal tertunda');
		expect(text).toContain('7 Desember 2026');
		expect(text).toContain('12.00');
		expect(text).toContain('WIB');
	});

	it('falls back to a plain delayed reveal when the moment is not a valid date', () => {
		expect(describePolicy({ shot_limit: null, reveal_mode: 'delayed', reveal_at: 'not-a-date' })).toBe(
			'Jepretan tak terbatas · Reveal tertunda'
		);
	});

	it('describes a delayed reveal that has no moment yet', () => {
		expect(describePolicy({ shot_limit: null, reveal_mode: 'delayed', reveal_at: null })).toBe(
			'Jepretan tak terbatas · Reveal tertunda'
		);
	});
});
