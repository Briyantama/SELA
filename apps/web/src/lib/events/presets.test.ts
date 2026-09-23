import { describe, expect, it } from 'vitest';
import type { Category } from '../api/events';
import { describeDefaults } from './presets';

const base: Category = {
	code: 'x',
	name: 'X',
	theme_key: 'neutral',
	shot_limit_default: 'tbd',
	default_shot_limit: null,
	reveal_default: 'tbd',
	default_reveal_delay_hours: null,
	effective_shot_limit: null,
	effective_reveal_mode: 'instant',
	effective_reveal_delay_hours: null
};

describe('describeDefaults', () => {
	it('describes the unlimited, instant fallback', () => {
		expect(describeDefaults(base)).toBe('Jepretan tak terbatas · Reveal langsung');
	});

	it('describes a per-guest shot limit with a delayed reveal', () => {
		const category: Category = {
			...base,
			effective_shot_limit: 25,
			effective_reveal_mode: 'delayed',
			effective_reveal_delay_hours: 48
		};
		expect(describeDefaults(category)).toBe('Jepretan 25 per tamu · Reveal tertunda 48 jam');
	});

	it('describes a delayed reveal whose delay is not defined yet', () => {
		const category: Category = { ...base, effective_reveal_mode: 'delayed' };
		expect(describeDefaults(category)).toBe('Jepretan tak terbatas · Reveal tertunda');
	});
});
