<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { requestOtp, verifyOtp } from '$lib/api/auth';
	import { ApiError } from '$lib/api/envelope';
	import { safeNextPath } from '$lib/auth/redirect';
	import { describeAuthError, formatWait } from '$lib/i18n/errors';

	const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+$/;
	const CODE_PATTERN = /^\d{6}$/;

	let step = $state<'email' | 'code'>('email');
	let email = $state('');
	let code = $state('');
	let error = $state('');
	let busy = $state(false);
	let coolingDown = $state(false);
	let validFor = $state('');
	let codeInput = $state<HTMLInputElement>();
	let cooldownTimer: ReturnType<typeof setTimeout> | undefined;

	$effect(() => {
		if (step === 'code') codeInput?.focus();
	});

	$effect(() => () => clearTimeout(cooldownTimer));

	/** Shows the failure and, when the server asked the host to wait, blocks resubmitting until then. */
	function fail(err: unknown) {
		error = describeAuthError(err);
		if (err instanceof ApiError && err.status === 429 && err.retryAfterSeconds) {
			coolingDown = true;
			clearTimeout(cooldownTimer);
			cooldownTimer = setTimeout(() => (coolingDown = false), err.retryAfterSeconds * 1000);
		}
	}

	async function submitEmail(event: SubmitEvent) {
		event.preventDefault();
		error = '';
		if (!EMAIL_PATTERN.test(email.trim())) {
			error = 'Masukkan alamat email yang valid.';
			return;
		}
		busy = true;
		try {
			const result = await requestOtp(email.trim());
			email = email.trim();
			validFor = formatWait(result.expiresInSeconds);
			code = '';
			step = 'code';
		} catch (err) {
			fail(err);
		} finally {
			busy = false;
		}
	}

	async function submitCode(event: SubmitEvent) {
		event.preventDefault();
		error = '';
		if (!CODE_PATTERN.test(code)) {
			error = 'Masukkan 6 digit kode.';
			return;
		}
		busy = true;
		try {
			await verifyOtp(email, code);
			await goto(safeNextPath(page.url.searchParams.get('next')));
		} catch (err) {
			fail(err);
		} finally {
			busy = false;
		}
	}

	function changeEmail() {
		step = 'email';
		code = '';
		error = '';
	}
</script>

<svelte:head>
	<title>Masuk · Sela</title>
</svelte:head>

<div class="stack">
	<header>
		<p class="eyebrow">Sela</p>
		<h1>Masuk untuk membuat acara</h1>
		<p class="lede">Kami kirim kode 6 digit ke email Anda. Tanpa kata sandi.</p>
	</header>

	<section class="card stack" aria-live="polite">
		{#if step === 'email'}
			<form class="stack" novalidate onsubmit={submitEmail}>
				<div class="field">
					<label for="email">Email</label>
					<input
						id="email"
						type="email"
						autocomplete="email"
						inputmode="email"
						placeholder="nama@contoh.com"
						bind:value={email}
						aria-invalid={error ? 'true' : undefined}
					/>
				</div>
				{#if error}<p class="alert" role="alert">{error}</p>{/if}
				<button class="btn btn-primary" type="submit" disabled={busy || coolingDown}>Kirim kode</button>
			</form>
		{:else}
			<form class="stack" novalidate onsubmit={submitCode}>
				<p>
					Kode dikirim ke <strong>{email}</strong>. Berlaku {validFor}.
				</p>
				<div class="field">
					<label for="code">Kode 6 digit</label>
					<input
						id="code"
						class="code-input"
						inputmode="numeric"
						autocomplete="one-time-code"
						maxlength="6"
						placeholder="000000"
						bind:this={codeInput}
						bind:value={code}
						aria-invalid={error ? 'true' : undefined}
					/>
				</div>
				{#if error}<p class="alert" role="alert">{error}</p>{/if}
				<button class="btn btn-primary" type="submit" disabled={busy || coolingDown}>Masuk</button>
				<button class="btn btn-quiet" type="button" onclick={changeEmail}>Ganti email</button>
			</form>
		{/if}
	</section>
</div>
