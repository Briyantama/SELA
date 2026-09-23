import { describe, expect, it } from 'vitest';
import config from '../vite.config';

type ConfigFn = (env: { mode: string; command: 'serve' | 'build' }) => {
	resolve?: { conditions?: string[] };
};

const resolveConfig = (mode: string, command: 'serve' | 'build') =>
	(config as unknown as ConfigFn)({ mode, command });

describe('vite config', () => {
	// An explicit `conditions: []` replaces Vite's defaults, drops "browser" and makes Svelte resolve
	// to its server build, where onMount never runs: the app renders but never loads any data.
	it.each([
		['development', 'serve'],
		['production', 'build']
	] as const)('leaves resolve.conditions at the Vite defaults in %s', (mode, command) => {
		expect(resolveConfig(mode, command).resolve?.conditions).toBeUndefined();
	});

	it('resolves Svelte to its browser build under test so components can mount', () => {
		expect(resolveConfig('test', 'serve').resolve?.conditions).toContain('browser');
	});
});
