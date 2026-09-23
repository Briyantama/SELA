import { describe, expect, it } from 'vitest';
import { createStepTimer } from './timing';

/** A clock the test moves by hand. */
function fakeClock() {
	let now = 0;
	return { now: () => now, advance: (ms: number) => (now += ms) };
}

describe('createStepTimer', () => {
	it('times each step and returns what the step returned', async () => {
		const clock = fakeClock();
		const timer = createStepTimer(clock.now);

		const value = await timer.step('login', async () => {
			clock.advance(1200);
			return 'signed in';
		});

		expect(value).toBe('signed in');
		expect(timer.report().steps).toEqual([{ name: 'login', ms: 1200 }]);
	});

	it('keeps steps in the order they ran and totals them', async () => {
		const clock = fakeClock();
		const timer = createStepTimer(clock.now);

		await timer.step('login', async () => void clock.advance(1500));
		await timer.step('category', async () => void clock.advance(400));
		await timer.step('form', async () => void clock.advance(2100));

		expect(timer.report()).toEqual({
			steps: [
				{ name: 'login', ms: 1500 },
				{ name: 'category', ms: 400 },
				{ name: 'form', ms: 2100 }
			],
			totalMs: 4000
		});
	});

	it('rounds to whole milliseconds', async () => {
		const clock = fakeClock();
		const timer = createStepTimer(clock.now);

		await timer.step('quick', async () => void clock.advance(12.6));

		expect(timer.report().steps[0].ms).toBe(13);
	});

	it('still records a step that throws, then rethrows', async () => {
		const clock = fakeClock();
		const timer = createStepTimer(clock.now);

		const outcome = await timer
			.step('broken', async () => {
				clock.advance(50);
				throw new Error('boom');
			})
			.then(
				() => 'resolved',
				(err: unknown) => (err as Error).message
			);

		expect(outcome).toBe('boom');
		expect(timer.report().steps).toEqual([{ name: 'broken', ms: 50 }]);
	});

	it('reports an empty run as zero', () => {
		expect(createStepTimer().report()).toEqual({ steps: [], totalMs: 0 });
	});
});
