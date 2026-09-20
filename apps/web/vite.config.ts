import adapter from '@sveltejs/adapter-auto';
import { sveltekit } from '@sveltejs/kit/vite';
import { loadEnv } from 'vite';
import { defineConfig } from 'vitest/config';

export default defineConfig(({ mode }) => ({
	server: {
		// The API sets the host session cookie for its own origin and has no CORS, so the browser
		// must reach it through the web app's origin. /e/* is deliberately not proxied: that path
		// belongs to the guest PWA.
		proxy: {
			'/api': {
				target: loadEnv(mode, '.', 'API_PROXY_').API_PROXY_TARGET ?? 'http://localhost:8080'
			}
		}
	},
	resolve: {
		// Svelte 5 components must resolve to their client build to mount in tests.
		conditions: mode === 'test' ? ['browser'] : []
	},
	test: {
		include: ['src/**/*.test.ts'],
		// Component tests opt in with a `// @vitest-environment jsdom` docblock.
		environment: 'node',
		coverage: {
			provider: 'v8',
			include: ['src/lib/**/*.{ts,svelte}', 'src/routes/**/*.svelte'],
			exclude: ['src/**/*.test.ts', 'src/**/*.d.ts', 'src/lib/index.ts', 'src/routes/+layout.svelte'],
			thresholds: { lines: 80, functions: 80, branches: 80, statements: 80 }
		}
	},
	plugins: [
		sveltekit({
			compilerOptions: {
				// Force runes mode for the project, except for libraries. Can be removed in svelte 6.
				runes: ({ filename }) =>
					filename.split(/[/\\]/).includes('node_modules') ? undefined : true
			},

			// adapter-auto only supports some environments, see https://svelte.dev/docs/kit/adapter-auto for a list.
			// If your environment is not supported, or you settled on a specific environment, switch out the adapter.
			// See https://svelte.dev/docs/kit/adapters for more information about adapters.
			adapter: adapter()
		})
	]
}));
