import { execFileSync } from 'node:child_process';

// Reaches the compose Redis through `docker exec ... redis-cli`, so the tests need no Redis client
// library. Only used by end-to-end tests against the local stack (deploy/docker-compose.yml).

const container = () => process.env.E2E_REDIS_CONTAINER ?? 'sela-dev-redis-1';

function redisCli(args: string[]): string {
	const password = process.env.REDIS_PASSWORD;
	if (!password) throw new Error('REDIS_PASSWORD is not set (scripts/e2e.sh exports it from .env)');
	// REDISCLI_AUTH keeps the password out of the process list; -e NAME forwards it from this env.
	return execFileSync('docker', ['exec', '-e', 'REDISCLI_AUTH', container(), 'redis-cli', '--raw', ...args], {
		encoding: 'utf8',
		env: { ...process.env, REDISCLI_AUTH: password }
	});
}

function scan(pattern: string): string[] {
	return redisCli(['--scan', '--pattern', pattern])
		.split('\n')
		.map((key) => key.trim())
		.filter(Boolean);
}

// Sign-in state that does not carry over between tests: request counters, failed attempts, lockouts.
// Issued codes (otp:) and sessions (session:) are left alone.
const RATE_LIMIT_PATTERNS = ['rl:*', 'attempts:*', 'lock:*'];

/** Clears the API's rate-limit, failed-attempt and lockout counters so tests cannot affect each other. */
export function resetRateLimits(): void {
	for (const pattern of RATE_LIMIT_PATTERNS) {
		const keys = scan(pattern);
		if (keys.length > 0) redisCli(['DEL', ...keys]);
	}
}

/** Everything stored under keys matching `pattern`, as text, for asserting what is (not) kept in Redis. */
export function dumpKeys(pattern: string): Record<string, string> {
	const dump: Record<string, string> = {};
	for (const key of scan(pattern)) {
		const type = redisCli(['TYPE', key]).trim();
		dump[key] = type === 'hash' ? redisCli(['HGETALL', key]) : type === 'string' ? redisCli(['GET', key]) : '';
	}
	return dump;
}
