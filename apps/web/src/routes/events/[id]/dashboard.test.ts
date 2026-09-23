// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '$lib/api/envelope';
import type { EventView } from '$lib/api/events';

const nav = vi.hoisted(() => ({ goto: vi.fn() }));
const eventsApi = vi.hoisted(() => ({
	getEvent: vi.fn(),
	qrUrl: (id: string, format: string) => `/api/v1/events/${id}/qr.${format}`
}));

vi.mock('$app/navigation', () => nav);
vi.mock('$lib/api/events', () => eventsApi);

import EventPage from './+page.svelte';

const event: EventView = {
	event_id: 'e1',
	category_code: 'wedding',
	name: 'Nikah A & B',
	event_date: '2026-12-05',
	timezone: 'Asia/Jakarta',
	status: 'active',
	short_code: 'aB3dE5gH',
	short_link: 'https://sela.test/e/aB3dE5gH',
	qr_png_url: '/api/v1/events/e1/qr.png',
	qr_svg_url: '/api/v1/events/e1/qr.svg',
	shot_limit: null,
	reveal_mode: 'delayed',
	reveal_at: '2026-12-07T05:00:00Z',
	package: 'free',
	created_at: '2026-09-20T00:00:00Z'
};

function stubClipboard(writeText: (text: string) => Promise<void>) {
	Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
}

beforeEach(() => {
	eventsApi.getEvent.mockReset().mockResolvedValue(event);
	nav.goto.mockReset();
});

afterEach(() => {
	Reflect.deleteProperty(navigator, 'clipboard');
});

describe('event page', () => {
	it('loads the event by the id in the route and shows its details', async () => {
		render(EventPage, { data: { id: 'e1', profile: null } });

		expect(await screen.findByRole('heading', { level: 1, name: 'Nikah A & B' })).toBeInTheDocument();
		expect(eventsApi.getEvent).toHaveBeenCalledWith('e1');
		expect(screen.getByText('Sabtu, 5 Desember 2026')).toBeInTheDocument();
		expect(screen.getByText(/Reveal tertunda/)).toBeInTheDocument();
	});

	it('loads the other event when the route id changes and forgets the old QR failure', async () => {
		eventsApi.getEvent.mockImplementation(async (id: string) => ({
			...event,
			event_id: id,
			name: id === 'e2' ? 'Ulang Tahun Rani' : event.name
		}));
		const { rerender } = render(EventPage, { data: { id: 'e1', profile: null } });
		await fireEvent.error(await screen.findByRole('img', { name: 'Kode QR untuk Nikah A & B' }));
		expect(await screen.findByRole('alert')).toHaveTextContent('Kode QR gagal dimuat.');

		await rerender({ data: { id: 'e2', profile: null } });

		expect(await screen.findByRole('heading', { level: 1, name: 'Ulang Tahun Rani' })).toBeInTheDocument();
		expect(eventsApi.getEvent).toHaveBeenLastCalledWith('e2');
		expect(screen.queryByRole('alert')).not.toBeInTheDocument();
	});

	it('shows the short link', async () => {
		render(EventPage, { data: { id: 'e1', profile: null } });

		expect(await screen.findByText('https://sela.test/e/aB3dE5gH')).toBeInTheDocument();
	});

	it('shows the QR image from the same-origin SVG endpoint', async () => {
		render(EventPage, { data: { id: 'e1', profile: null } });

		const img = await screen.findByRole('img', { name: 'Kode QR untuk Nikah A & B' });

		expect(img).toHaveAttribute('src', '/api/v1/events/e1/qr.svg');
	});

	it('offers PNG and SVG downloads with meaningful file names', async () => {
		render(EventPage, { data: { id: 'e1', profile: null } });

		const png = await screen.findByRole('link', { name: 'Unduh PNG' });
		const svg = screen.getByRole('link', { name: 'Unduh SVG' });

		expect(png).toHaveAttribute('href', '/api/v1/events/e1/qr.png');
		expect(png).toHaveAttribute('download', 'sela-aB3dE5gH.png');
		expect(svg).toHaveAttribute('href', '/api/v1/events/e1/qr.svg');
		expect(svg).toHaveAttribute('download', 'sela-aB3dE5gH.svg');
	});

	it('says so when the QR image cannot be loaded', async () => {
		render(EventPage, { data: { id: 'e1', profile: null } });
		const img = await screen.findByRole('img', { name: 'Kode QR untuk Nikah A & B' });

		await fireEvent.error(img);

		expect(await screen.findByRole('alert')).toHaveTextContent('Kode QR gagal dimuat.');
	});

	it('copies the short link and confirms it', async () => {
		const writeText = vi.fn().mockResolvedValue(undefined);
		stubClipboard(writeText);
		render(EventPage, { data: { id: 'e1', profile: null } });

		await fireEvent.click(await screen.findByRole('button', { name: 'Salin tautan' }));

		expect(writeText).toHaveBeenCalledWith('https://sela.test/e/aB3dE5gH');
		expect(await screen.findByText('Tautan tersalin.')).toBeInTheDocument();
	});

	it('asks for a manual copy when the clipboard is refused', async () => {
		stubClipboard(() => Promise.reject(new Error('denied')));
		render(EventPage, { data: { id: 'e1', profile: null } });

		await fireEvent.click(await screen.findByRole('button', { name: 'Salin tautan' }));

		expect(await screen.findByText('Tidak bisa menyalin. Salin tautan secara manual.')).toBeInTheDocument();
	});

	it('asks for a manual copy when there is no clipboard API', async () => {
		render(EventPage, { data: { id: 'e1', profile: null } });

		await fireEvent.click(await screen.findByRole('button', { name: 'Salin tautan' }));

		expect(await screen.findByText('Tidak bisa menyalin. Salin tautan secara manual.')).toBeInTheDocument();
	});

	it('links to creating another event', async () => {
		render(EventPage, { data: { id: 'e1', profile: null } });

		expect(await screen.findByRole('link', { name: 'Buat acara lain' })).toHaveAttribute(
			'href',
			'/events/new'
		);
	});
});

describe('event page failures', () => {
	it('says the event was not found and offers to create one', async () => {
		eventsApi.getEvent.mockRejectedValue(new ApiError('event not found', 404));
		render(EventPage, { data: { id: 'nope', profile: null } });

		expect(await screen.findByRole('alert')).toHaveTextContent('Acara tidak ditemukan.');
		expect(screen.getByRole('link', { name: 'Buat acara baru' })).toHaveAttribute('href', '/events/new');
	});

	it('sends a signed-out host to sign in and back to this event', async () => {
		eventsApi.getEvent.mockRejectedValue(new ApiError('authentication required', 401));
		render(EventPage, { data: { id: 'e 1', profile: null } });

		await waitFor(() =>
			expect(nav.goto).toHaveBeenCalledWith(`/auth/login?next=${encodeURIComponent('/events/e%201')}`)
		);
	});

	it('offers a retry after a network failure', async () => {
		eventsApi.getEvent.mockRejectedValueOnce(new ApiError('Network request failed', 0));
		render(EventPage, { data: { id: 'e1', profile: null } });

		expect(await screen.findByRole('alert')).toHaveTextContent(
			'Tidak dapat terhubung ke server. Periksa koneksi Anda.'
		);
		await fireEvent.click(screen.getByRole('button', { name: 'Coba lagi' }));

		expect(await screen.findByRole('heading', { level: 1, name: 'Nikah A & B' })).toBeInTheDocument();
	});
});
