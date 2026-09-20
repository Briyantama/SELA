// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ApiError } from '$lib/api/envelope';

const nav = vi.hoisted(() => ({ goto: vi.fn() }));
const pageState = vi.hoisted(() => ({ url: new URL('http://localhost/auth/login') }));
const authApi = vi.hoisted(() => ({ requestOtp: vi.fn(), verifyOtp: vi.fn() }));

vi.mock('$app/navigation', () => nav);
vi.mock('$app/state', () => ({ page: pageState }));
vi.mock('$lib/api/auth', () => authApi);

import LoginPage from './+page.svelte';

const EMAIL = 'host@example.test';

async function submitEmail(email = EMAIL) {
	await fireEvent.input(screen.getByLabelText('Email'), { target: { value: email } });
	await fireEvent.click(screen.getByRole('button', { name: 'Kirim kode' }));
}

async function reachCodeStep() {
	authApi.requestOtp.mockResolvedValue({ expiresInSeconds: 300 });
	render(LoginPage);
	await submitEmail();
	return screen.findByLabelText('Kode 6 digit');
}

async function submitCode(code: string) {
	await fireEvent.input(screen.getByLabelText('Kode 6 digit'), { target: { value: code } });
	await fireEvent.click(screen.getByRole('button', { name: 'Masuk' }));
}

beforeEach(() => {
	authApi.requestOtp.mockReset();
	authApi.verifyOtp.mockReset();
	nav.goto.mockReset();
	pageState.url = new URL('http://localhost/auth/login');
});

afterEach(() => {
	vi.useRealTimers();
});

describe('login page: email step', () => {
	it('asks for the email first', () => {
		render(LoginPage);

		expect(screen.getByRole('heading', { level: 1 })).toBeInTheDocument();
		expect(screen.getByLabelText('Email')).toHaveAttribute('type', 'email');
		expect(screen.queryByLabelText('Kode 6 digit')).not.toBeInTheDocument();
	});

	it('requests a code and moves to the code step', async () => {
		authApi.requestOtp.mockResolvedValue({ expiresInSeconds: 300 });
		render(LoginPage);

		await submitEmail();

		expect(authApi.requestOtp).toHaveBeenCalledWith(EMAIL);
		expect(await screen.findByLabelText('Kode 6 digit')).toBeInTheDocument();
		expect(screen.getByText(EMAIL)).toBeInTheDocument();
	});

	it('does not call the API for an empty or malformed email', async () => {
		render(LoginPage);

		await submitEmail('');
		expect(await screen.findByRole('alert')).toHaveTextContent('Masukkan alamat email yang valid.');
		await submitEmail('bukan-email');

		expect(authApi.requestOtp).not.toHaveBeenCalled();
	});

	it('shows the localised message when the server rejects the email', async () => {
		authApi.requestOtp.mockRejectedValue(new ApiError('invalid email address', 400));
		render(LoginPage);

		await submitEmail();

		expect(await screen.findByRole('alert')).toHaveTextContent('Alamat email tidak valid.');
		expect(screen.queryByLabelText('Kode 6 digit')).not.toBeInTheDocument();
	});

	it('shows the wait time after a rate limit and blocks resubmitting until it passes', async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		authApi.requestOtp.mockRejectedValue(new ApiError('too many requests', 429, 90));
		render(LoginPage);

		await submitEmail();

		expect(await screen.findByRole('alert')).toHaveTextContent(
			'Terlalu banyak permintaan. Coba lagi dalam 2 menit.'
		);
		const button = screen.getByRole('button', { name: 'Kirim kode' });
		expect(button).toBeDisabled();

		await vi.advanceTimersByTimeAsync(90_000);
		await waitFor(() => expect(button).toBeEnabled());
	});
});

describe('login page: code step', () => {
	it('rejects a code that is not six digits without calling the API', async () => {
		await reachCodeStep();

		await submitCode('12ab');

		expect(await screen.findByRole('alert')).toHaveTextContent('Masukkan 6 digit kode.');
		expect(authApi.verifyOtp).not.toHaveBeenCalled();
	});

	it('accepts a code pasted with spaces around or inside the digits', async () => {
		authApi.verifyOtp.mockResolvedValue({ hostId: 'h1', isNewHost: false });
		await reachCodeStep();

		await submitCode(' 123 456 ');

		expect(authApi.verifyOtp).toHaveBeenCalledWith(EMAIL, '123456');
	});

	it('verifies the code and goes to the default page', async () => {
		authApi.verifyOtp.mockResolvedValue({ hostId: 'h1', isNewHost: false });
		await reachCodeStep();

		await submitCode('123456');

		expect(authApi.verifyOtp).toHaveBeenCalledWith(EMAIL, '123456');
		await waitFor(() => expect(nav.goto).toHaveBeenCalledWith('/events/new'));
	});

	it('returns to the page the host was sent from', async () => {
		pageState.url = new URL('http://localhost/auth/login?next=%2Fevents%2Fabc-123');
		authApi.verifyOtp.mockResolvedValue({ hostId: 'h1', isNewHost: false });
		await reachCodeStep();

		await submitCode('123456');

		await waitFor(() => expect(nav.goto).toHaveBeenCalledWith('/events/abc-123'));
	});

	it('ignores an off-site next parameter', async () => {
		pageState.url = new URL('http://localhost/auth/login?next=%2F%2Fevil.example');
		authApi.verifyOtp.mockResolvedValue({ hostId: 'h1', isNewHost: false });
		await reachCodeStep();

		await submitCode('123456');

		await waitFor(() => expect(nav.goto).toHaveBeenCalledWith('/events/new'));
	});

	it('shows a wrong-code message and stays on the code step', async () => {
		authApi.verifyOtp.mockRejectedValue(new ApiError('invalid or expired code', 401));
		await reachCodeStep();

		await submitCode('000000');

		expect(await screen.findByRole('alert')).toHaveTextContent('Kode salah atau sudah kedaluwarsa.');
		expect(screen.getByLabelText('Kode 6 digit')).toBeInTheDocument();
		expect(nav.goto).not.toHaveBeenCalled();
	});

	it('shows the lockout wait and disables sign-in after too many wrong codes', async () => {
		authApi.verifyOtp.mockRejectedValue(new ApiError('too many failed attempts', 429, 900));
		await reachCodeStep();

		await submitCode('000000');

		expect(await screen.findByRole('alert')).toHaveTextContent(
			'Terlalu banyak percobaan gagal. Coba lagi dalam 15 menit.'
		);
		expect(screen.getByRole('button', { name: 'Masuk' })).toBeDisabled();
	});

	it('lets the host go back and change the email', async () => {
		await reachCodeStep();

		await fireEvent.click(screen.getByRole('button', { name: 'Ganti email' }));

		expect(await screen.findByLabelText('Email')).toHaveValue(EMAIL);
		expect(screen.queryByLabelText('Kode 6 digit')).not.toBeInTheDocument();
	});
});
