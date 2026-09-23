import { redirect } from '@sveltejs/kit';

// There is no host home yet; signed-in hosts start by creating an event.
export function load(): never {
	redirect(307, '/events/new');
}
