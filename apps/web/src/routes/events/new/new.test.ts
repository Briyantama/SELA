// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Category } from '$lib/api/events';
import { ApiError } from '$lib/api/envelope';

const nav = vi.hoisted(() => ({ goto: vi.fn() }));
const eventsApi = vi.hoisted(() => ({ listCategories: vi.fn(), createEvent: vi.fn() }));

vi.mock('$app/navigation', () => nav);
vi.mock('$lib/api/events', () => eventsApi);

import NewEventPage from './+page.svelte';

const birthday: Category = {
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

const wedding: Category = {
	...birthday,
	code: 'wedding',
	name: 'Pernikahan',
	theme_key: 'gold',
	effective_reveal_mode: 'delayed',
	effective_reveal_delay_hours: 48
};

async function pickCategory(name: RegExp) {
	await fireEvent.click(await screen.findByRole('radio', { name }));
	await fireEvent.click(screen.getByRole('button', { name: 'Lanjut' }));
	return screen.findByLabelText('Nama acara');
}

async function fillDetails(name = 'Pesta Rani', date = '2026-12-05') {
	await fireEvent.input(screen.getByLabelText('Nama acara'), { target: { value: name } });
	await fireEvent.input(screen.getByLabelText('Tanggal acara'), { target: { value: date } });
}

const submit = () => fireEvent.click(screen.getByRole('button', { name: 'Buat acara' }));

beforeEach(() => {
	eventsApi.listCategories.mockReset().mockResolvedValue([wedding, birthday]);
	eventsApi.createEvent.mockReset().mockResolvedValue({ event_id: 'e1' });
	nav.goto.mockReset();
});

describe('new event: category step', () => {
	it('lists the categories with their effective defaults and waits for a choice', async () => {
		render(NewEventPage);

		const radio = await screen.findByRole('radio', { name: /Pernikahan/ });
		expect(radio).toBeInTheDocument();
		expect(screen.getByText('Jepretan tak terbatas · Reveal tertunda 48 jam')).toBeInTheDocument();
		expect(screen.getByRole('radio', { name: /Ulang Tahun/ })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Lanjut' })).toBeDisabled();
	});

	it('offers a retry when the categories cannot be loaded', async () => {
		eventsApi.listCategories.mockRejectedValueOnce(new ApiError('Network request failed', 0));
		render(NewEventPage);

		expect(await screen.findByRole('alert')).toHaveTextContent(
			'Tidak dapat terhubung ke server. Periksa koneksi Anda.'
		);
		await fireEvent.click(screen.getByRole('button', { name: 'Coba lagi' }));

		expect(await screen.findByRole('radio', { name: /Pernikahan/ })).toBeInTheDocument();
		expect(eventsApi.listCategories).toHaveBeenCalledTimes(2);
	});
});

describe('new event: details step', () => {
	it('shows the chosen preset, a fixed WIB time zone and collapsed advanced settings', async () => {
		render(NewEventPage);

		await pickCategory(/Pernikahan/);

		expect(screen.getByText(/Pernikahan/, { selector: '.chosen *, .chosen' })).toBeInTheDocument();
		expect(screen.getByLabelText('Zona waktu')).toHaveValue('WIB (Asia/Jakarta)');
		expect(screen.getByLabelText('Zona waktu')).toHaveAttribute('readonly');
		const advanced = screen.getByText('Pengaturan lanjutan').closest('details');
		expect(advanced).not.toHaveAttribute('open');
	});

	it('lets the host go back to the categories with the choice kept', async () => {
		render(NewEventPage);
		await pickCategory(/Pernikahan/);

		await fireEvent.click(screen.getByRole('button', { name: 'Kembali' }));

		expect(await screen.findByRole('radio', { name: /Pernikahan/ })).toBeChecked();
	});

	it('shows field errors and does not call the API for an empty name', async () => {
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		await fireEvent.input(screen.getByLabelText('Tanggal acara'), { target: { value: '2026-12-05' } });

		await submit();

		expect(await screen.findByText('Nama acara wajib diisi.')).toBeInTheDocument();
		expect(screen.getByLabelText('Nama acara')).toHaveAttribute('aria-invalid', 'true');
		expect(eventsApi.createEvent).not.toHaveBeenCalled();
	});

	it('creates the event with only the required fields and opens its page', async () => {
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		await fillDetails();

		await submit();

		await waitFor(() => expect(nav.goto).toHaveBeenCalledWith('/events/e1'));
		expect(eventsApi.createEvent).toHaveBeenCalledWith({
			category_code: 'birthday',
			name: 'Pesta Rani',
			event_date: '2026-12-05'
		});
	});

	it('sends the advanced overrides the host filled in', async () => {
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		await fillDetails();
		await fireEvent.change(screen.getByLabelText('Mode reveal'), { target: { value: 'delayed' } });
		await fireEvent.input(screen.getByLabelText('Jeda reveal (jam)'), { target: { value: '12' } });
		await fireEvent.input(screen.getByLabelText('Batas jepretan per tamu'), { target: { value: '30' } });

		await submit();

		await waitFor(() =>
			expect(eventsApi.createEvent).toHaveBeenCalledWith({
				category_code: 'birthday',
				name: 'Pesta Rani',
				event_date: '2026-12-05',
				shot_limit: 30,
				reveal_mode: 'delayed',
				reveal_delay_hours: 12
			})
		);
	});

	it('shows the reveal delay only while the effective reveal is delayed', async () => {
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		expect(screen.queryByLabelText('Jeda reveal (jam)')).not.toBeInTheDocument();

		await fireEvent.change(screen.getByLabelText('Mode reveal'), { target: { value: 'delayed' } });
		expect(await screen.findByLabelText('Jeda reveal (jam)')).toBeInTheDocument();

		await fireEvent.change(screen.getByLabelText('Mode reveal'), { target: { value: 'instant' } });
		await waitFor(() => expect(screen.queryByLabelText('Jeda reveal (jam)')).not.toBeInTheDocument());
	});

	it('drops a reveal delay typed earlier once the host switches back to an instant reveal', async () => {
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		await fillDetails();
		await fireEvent.change(screen.getByLabelText('Mode reveal'), { target: { value: 'delayed' } });
		await fireEvent.input(screen.getByLabelText('Jeda reveal (jam)'), { target: { value: '24' } });
		await fireEvent.change(screen.getByLabelText('Mode reveal'), { target: { value: 'instant' } });

		await submit();

		await waitFor(() =>
			expect(eventsApi.createEvent).toHaveBeenCalledWith({
				category_code: 'birthday',
				name: 'Pesta Rani',
				event_date: '2026-12-05',
				reveal_mode: 'instant'
			})
		);
	});

	it('keeps the name and date but resets the overrides when another category is chosen', async () => {
		render(NewEventPage);
		await pickCategory(/Pernikahan/);
		await fillDetails();
		await fireEvent.input(screen.getByLabelText('Batas jepretan per tamu'), { target: { value: '30' } });
		await fireEvent.click(screen.getByRole('button', { name: 'Kembali' }));

		await fireEvent.click(await screen.findByRole('radio', { name: /Ulang Tahun/ }));
		await fireEvent.click(screen.getByRole('button', { name: 'Lanjut' }));

		expect(await screen.findByLabelText('Nama acara')).toHaveValue('Pesta Rani');
		expect(screen.getByLabelText('Batas jepretan per tamu')).toHaveValue('');
	});

	it('does not offer a second create when the event exists but opening it failed', async () => {
		nav.goto.mockImplementation(async () => {
			throw new Error('navigation failed');
		});
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		await fillDetails();

		await submit();

		expect(await screen.findByRole('link', { name: 'Buka acara' })).toHaveAttribute('href', '/events/e1');
		expect(screen.getByRole('button', { name: 'Buat acara' })).toBeDisabled();
		expect(eventsApi.createEvent).toHaveBeenCalledTimes(1);
	});

	it('sends a signed-out host to the login page and back here afterwards', async () => {
		eventsApi.createEvent.mockRejectedValue(new ApiError('authentication required', 401));
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		await fillDetails();

		await submit();

		await waitFor(() =>
			expect(nav.goto).toHaveBeenCalledWith(`/auth/login?next=${encodeURIComponent('/events/new')}`)
		);
	});

	it('shows the server validation message and keeps what the host typed', async () => {
		eventsApi.createEvent.mockRejectedValue(new ApiError('name contains invalid characters', 400));
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		await fillDetails('Pesta Rani');

		await submit();

		expect(await screen.findByRole('alert')).toHaveTextContent(
			'Data acara ditolak: name contains invalid characters'
		);
		expect(screen.getByLabelText('Nama acara')).toHaveValue('Pesta Rani');
		expect(nav.goto).not.toHaveBeenCalled();
	});

	it('prevents a second submit while the first is in flight', async () => {
		let release: (value: unknown) => void = () => {};
		eventsApi.createEvent.mockReturnValue(new Promise((resolve) => (release = resolve)));
		render(NewEventPage);
		await pickCategory(/Ulang Tahun/);
		await fillDetails();

		await submit();
		await waitFor(() => expect(screen.getByRole('button', { name: 'Buat acara' })).toBeDisabled());
		await submit();
		release({ event_id: 'e1' });

		await waitFor(() => expect(nav.goto).toHaveBeenCalledTimes(1));
		expect(eventsApi.createEvent).toHaveBeenCalledTimes(1);
	});
});
