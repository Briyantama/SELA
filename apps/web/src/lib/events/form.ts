import type { Category, CreateEventBody, RevealMode } from '../api/events';

/** Raw input values. For the override fields an empty string means "use the category default". */
export interface EventFormValues {
	name: string;
	/** YYYY-MM-DD, as produced by <input type="date">. */
	eventDate: string;
	shotLimit: string;
	revealMode: RevealMode | '';
	revealDelayHours: string;
}

export type EventFormField = 'name' | 'eventDate' | 'shotLimit' | 'revealDelayHours';
export type EventFormErrors = Partial<Record<EventFormField, string>>;

// Limits mirror the server's validation (services/event/service.go) so the host sees problems
// before submitting; the server remains the authority.
const MAX_NAME_CHARACTERS = 200;
const MAX_REVEAL_DELAY_HOURS = 24 * 365;

const WHOLE_NUMBER = /^\d+$/;
const ISO_DATE = /^(\d{4})-(\d{2})-(\d{2})$/;

const messages = {
	nameRequired: 'Nama acara wajib diisi.',
	nameTooLong: `Nama acara maksimal ${MAX_NAME_CHARACTERS} karakter.`,
	dateInvalid: 'Pilih tanggal acara yang valid.',
	shotLimitInvalid: 'Isi dengan angka bulat 0 atau lebih (0 = tak terbatas).',
	delayRequired: 'Isi jeda reveal dalam jam untuk reveal tertunda.',
	delayInvalid: `Isi jeda antara 1 dan ${MAX_REVEAL_DELAY_HOURS} jam.`,
	delayNotApplicable: 'Jeda reveal hanya berlaku untuk reveal tertunda.'
};

function isRealDate(value: string): boolean {
	const match = ISO_DATE.exec(value);
	if (!match) return false;
	const [year, month, day] = [Number(match[1]), Number(match[2]), Number(match[3])];
	const date = new Date(Date.UTC(year, month - 1, day));
	return (
		date.getUTCFullYear() === year && date.getUTCMonth() === month - 1 && date.getUTCDate() === day
	);
}

function validateDelay(values: EventFormValues, category: Category): string | undefined {
	const delay = values.revealDelayHours.trim();
	const mode = values.revealMode || category.effective_reveal_mode;

	if (delay === '') {
		const needsDelay =
			values.revealMode === 'delayed' && category.effective_reveal_delay_hours === null;
		return needsDelay ? messages.delayRequired : undefined;
	}
	if (mode !== 'delayed') return messages.delayNotApplicable;

	const hours = Number(delay);
	const valid = WHOLE_NUMBER.test(delay) && hours >= 1 && hours <= MAX_REVEAL_DELAY_HOURS;
	return valid ? undefined : messages.delayInvalid;
}

/** Returns a message per invalid field; an empty object means the form can be submitted. */
export function validateEventForm(values: EventFormValues, category: Category): EventFormErrors {
	const errors: EventFormErrors = {};

	const name = values.name.trim();
	if (name === '') errors.name = messages.nameRequired;
	else if (Array.from(name).length > MAX_NAME_CHARACTERS) errors.name = messages.nameTooLong;

	if (!isRealDate(values.eventDate)) errors.eventDate = messages.dateInvalid;

	const shotLimit = values.shotLimit.trim();
	if (shotLimit !== '' && !(WHOLE_NUMBER.test(shotLimit) && Number.isSafeInteger(Number(shotLimit)))) {
		errors.shotLimit = messages.shotLimitInvalid;
	}

	const delayError = validateDelay(values, category);
	if (delayError) errors.revealDelayHours = delayError;

	return errors;
}

/** Builds the API body, sending an override only when the host filled it in. */
export function buildCreateBody(categoryCode: string, values: EventFormValues): CreateEventBody {
	const body: CreateEventBody = {
		category_code: categoryCode,
		name: values.name.trim(),
		event_date: values.eventDate
	};
	if (values.shotLimit.trim() !== '') body.shot_limit = Number(values.shotLimit);
	if (values.revealMode !== '') body.reveal_mode = values.revealMode;
	if (values.revealDelayHours.trim() !== '') body.reveal_delay_hours = Number(values.revealDelayHours);
	return body;
}
