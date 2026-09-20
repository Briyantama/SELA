import { describe, expect, it } from 'vitest';
import type { Category } from '../api/events';
import { buildCreateBody, validateEventForm, type EventFormValues } from './form';

const instantCategory: Category = {
	code: 'birthday',
	name: 'Ulang Tahun',
	theme_key: 'warm',
	shot_limit_default: 'tbd',
	default_shot_limit: null,
	reveal_default: 'tbd',
	default_reveal_delay_hours: null,
	effective_shot_limit: null,
	effective_reveal_mode: 'instant',
	effective_reveal_delay_hours: null
};

const delayedCategory: Category = {
	...instantCategory,
	code: 'wedding',
	name: 'Pernikahan',
	reveal_default: 'delayed',
	default_reveal_delay_hours: 48,
	effective_reveal_mode: 'delayed',
	effective_reveal_delay_hours: 48
};

function values(overrides: Partial<EventFormValues> = {}): EventFormValues {
	return {
		name: 'Nikah A & B',
		eventDate: '2026-12-05',
		shotLimit: '',
		revealMode: '',
		revealDelayHours: '',
		...overrides
	};
}

describe('validateEventForm', () => {
	it('accepts a name and date with every override left at its default', () => {
		expect(validateEventForm(values(), instantCategory)).toEqual({});
	});

	it.each([
		['empty name', { name: '' }],
		['blank name', { name: '   ' }]
	])('rejects an %s', (_label, override) => {
		expect(validateEventForm(values(override), instantCategory)).toHaveProperty('name');
	});

	it('counts characters, not UTF-16 units, against the 200 limit', () => {
		expect(validateEventForm(values({ name: '😀'.repeat(200) }), instantCategory)).toEqual({});
		expect(validateEventForm(values({ name: 'a'.repeat(201) }), instantCategory)).toHaveProperty('name');
	});

	it.each(['', '2026-13-01', '2026-02-30', '05-12-2026', '2026-1-5', 'besok'])(
		'rejects the event date %j',
		(eventDate) => {
			expect(validateEventForm(values({ eventDate }), instantCategory)).toHaveProperty('eventDate');
		}
	);

	it('accepts a leap day', () => {
		expect(validateEventForm(values({ eventDate: '2028-02-29' }), instantCategory)).toEqual({});
	});

	it.each([
		['0', true],
		['25', true],
		['-1', false],
		['1.5', false],
		['abc', false],
		['1e3', false]
	])('shot limit %j valid=%s', (shotLimit, valid) => {
		const errors = validateEventForm(values({ shotLimit }), instantCategory);
		expect('shotLimit' in errors).toBe(!valid);
	});

	it('requires a delay when the host picks a delayed reveal and the category has no default delay', () => {
		const errors = validateEventForm(values({ revealMode: 'delayed' }), instantCategory);
		expect(errors).toHaveProperty('revealDelayHours');
	});

	it('does not require a delay when the category already defines one', () => {
		expect(validateEventForm(values({ revealMode: 'delayed' }), delayedCategory)).toEqual({});
	});

	it.each([
		['0', false],
		['1', true],
		['8760', true],
		['8761', false],
		['2.5', false]
	])('delay %j valid=%s for a delayed reveal', (revealDelayHours, valid) => {
		const errors = validateEventForm(values({ revealMode: 'delayed', revealDelayHours }), instantCategory);
		expect('revealDelayHours' in errors).toBe(!valid);
	});

	it('rejects a delay when the effective reveal is instant', () => {
		const errors = validateEventForm(values({ revealDelayHours: '24' }), instantCategory);
		expect(errors).toHaveProperty('revealDelayHours');
	});

	it('rejects a delay when the host picks instant over a delayed category', () => {
		const errors = validateEventForm(
			values({ revealMode: 'instant', revealDelayHours: '24' }),
			delayedCategory
		);
		expect(errors).toHaveProperty('revealDelayHours');
	});

	it('accepts a delay when the category default is delayed and the host only changes the hours', () => {
		expect(validateEventForm(values({ revealDelayHours: '72' }), delayedCategory)).toEqual({});
	});
});

describe('buildCreateBody', () => {
	it('sends only the required fields when nothing is overridden, with the name trimmed', () => {
		expect(buildCreateBody('birthday', values({ name: '  Pesta Rani  ' }))).toEqual({
			category_code: 'birthday',
			name: 'Pesta Rani',
			event_date: '2026-12-05'
		});
	});

	it('sends each override the host filled in as a number or mode', () => {
		const body = buildCreateBody(
			'wedding',
			values({ shotLimit: '0', revealMode: 'delayed', revealDelayHours: '24' })
		);
		expect(body).toEqual({
			category_code: 'wedding',
			name: 'Nikah A & B',
			event_date: '2026-12-05',
			shot_limit: 0,
			reveal_mode: 'delayed',
			reveal_delay_hours: 24
		});
	});
});
