import type { Category } from '../api/events';

/**
 * One-line summary of what a new event gets from this category when the host overrides nothing,
 * e.g. "Jepretan tak terbatas · Reveal langsung". Uses the API's effective defaults, so categories
 * whose product defaults are still undefined read as the safe fallback.
 */
export function describeDefaults(category: Category): string {
	const shots =
		category.effective_shot_limit === null
			? 'Jepretan tak terbatas'
			: `Jepretan ${category.effective_shot_limit} per tamu`;

	let reveal = 'Reveal langsung';
	if (category.effective_reveal_mode === 'delayed') {
		reveal =
			category.effective_reveal_delay_hours === null
				? 'Reveal tertunda'
				: `Reveal tertunda ${category.effective_reveal_delay_hours} jam`;
	}
	return `${shots} · ${reveal}`;
}
