export interface StepTiming {
	name: string;
	ms: number;
}

export interface TimingReport {
	steps: StepTiming[];
	totalMs: number;
}

/**
 * Times the named phases of a scripted run. The clock is injectable so the arithmetic can be
 * tested; by default it is performance.now().
 */
export function createStepTimer(now: () => number = () => performance.now()) {
	const steps: StepTiming[] = [];

	return {
		/** Runs `fn`, records how long it took (even if it throws) and returns its result. */
		async step<T>(name: string, fn: () => Promise<T>): Promise<T> {
			const start = now();
			try {
				return await fn();
			} finally {
				steps.push({ name, ms: Math.round(now() - start) });
			}
		},

		report(): TimingReport {
			return { steps: [...steps], totalMs: steps.reduce((sum, s) => sum + s.ms, 0) };
		}
	};
}
