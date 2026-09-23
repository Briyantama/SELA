import type { EventView } from '../api/events';

const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})$/;

// Explicit fields rather than dateStyle: 'full', which zero-pads the day ("05 Desember") in id-ID.
const dateFormat = new Intl.DateTimeFormat('id-ID', {
	weekday: 'long',
	day: 'numeric',
	month: 'long',
	year: 'numeric',
	timeZone: 'UTC'
});
const momentFormat = new Intl.DateTimeFormat('id-ID', {
	dateStyle: 'long',
	timeStyle: 'short',
	timeZone: 'Asia/Jakarta'
});

/**
 * Writes an event's YYYY-MM-DD date in Indonesian ("Sabtu, 5 Desember 2026"). The date is a
 * calendar day, not a moment, so it is formatted in UTC to keep the runtime time zone out of it.
 */
export function formatEventDate(isoDate: string): string {
	const match = ISO_DATE.exec(isoDate);
	if (!match) return isoDate;
	const date = new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3])));
	return Number.isNaN(date.getTime()) ? isoDate : dateFormat.format(date);
}

/** One-line summary of an event's shot limit and reveal setting, e.g. "Jepretan 30 per tamu · Reveal langsung". */
export function describePolicy(event: Pick<EventView, 'shot_limit' | 'reveal_mode' | 'reveal_at'>): string {
	const shots =
		event.shot_limit === null ? 'Jepretan tak terbatas' : `Jepretan ${event.shot_limit} per tamu`;

	let reveal = 'Reveal langsung';
	if (event.reveal_mode === 'delayed') {
		const at = event.reveal_at ? new Date(event.reveal_at) : undefined;
		// A malformed timestamp must not make Intl throw and blank the page during render.
		reveal =
			at && !Number.isNaN(at.getTime())
				? `Reveal tertunda, dibuka ${momentFormat.format(at)} WIB`
				: 'Reveal tertunda';
	}
	return `${shots} · ${reveal}`;
}
