<script lang="ts">
	import { goto } from '$app/navigation';
	import { untrack } from 'svelte';
	import { ApiError } from '$lib/api/envelope';
	import { getEvent, qrUrl, type EventView } from '$lib/api/events';
	import CopyButton from '$lib/components/CopyButton.svelte';
	import { describePolicy, formatEventDate } from '$lib/events/format';
	import { describeEventError } from '$lib/i18n/errors';
	import type { PageData } from './$types';

	let { data }: { data: PageData } = $props();

	let event = $state<EventView>();
	let error = $state('');
	let notFound = $state(false);
	let qrFailed = $state(false);

	let latestRequest = 0;

	async function load(id: string) {
		// The same component instance serves /events/A then /events/B, so start from a clean slate
		// and ignore a slower response that belongs to an id the host has already left.
		const request = ++latestRequest;
		event = undefined;
		error = '';
		notFound = false;
		qrFailed = false;
		try {
			const loaded = await getEvent(id);
			if (request === latestRequest) event = loaded;
		} catch (err) {
			if (request !== latestRequest) return;
			if (err instanceof ApiError && err.status === 401) {
				await goto(`/auth/login?next=${encodeURIComponent(`/events/${encodeURIComponent(id)}`)}`);
				return;
			}
			notFound = err instanceof ApiError && err.status === 404;
			error = describeEventError(err);
		}
	}

	$effect(() => {
		const id = data.id;
		untrack(() => load(id));
	});
</script>

<svelte:head>
	<title>{event ? `${event.name} · Sela` : 'Acara · Sela'}</title>
</svelte:head>

{#if event}
	<div class="stack">
		<header>
			<p class="eyebrow">Acara siap</p>
			<h1>{event.name}</h1>
			<p class="lede">{formatEventDate(event.event_date)}</p>
			<p class="hint policy">{describePolicy(event)}</p>
		</header>

		<section class="card stack" aria-labelledby="link-heading">
			<h2 id="link-heading">Tautan untuk tamu</h2>
			<code class="link">{event.short_link}</code>
			<CopyButton
				text={event.short_link}
				label="Salin tautan"
				done="Tautan tersalin."
				failed="Tidak bisa menyalin. Salin tautan secara manual."
			/>
		</section>

		<section class="card stack" aria-labelledby="qr-heading">
			<h2 id="qr-heading">Kode QR</h2>
			<p class="hint">Cetak atau tampilkan kode ini di lokasi acara. Tamu cukup memindainya.</p>
			{#if qrFailed}
				<p class="alert" role="alert">Kode QR gagal dimuat.</p>
			{/if}
			<img
				class="qr"
				src={qrUrl(event.event_id, 'svg')}
				alt={`Kode QR untuk ${event.name}`}
				width="280"
				height="280"
				onerror={() => (qrFailed = true)}
			/>
			<div class="downloads">
				<a class="btn" href={qrUrl(event.event_id, 'png')} download={`sela-${event.short_code}.png`}
					>Unduh PNG</a
				>
				<a class="btn" href={qrUrl(event.event_id, 'svg')} download={`sela-${event.short_code}.svg`}
					>Unduh SVG</a
				>
			</div>
		</section>

		<a class="btn btn-quiet" href="/events/new">Buat acara lain</a>
	</div>
{:else if error}
	<div class="card stack">
		<p class="alert" role="alert">{error}</p>
		{#if notFound}
			<a class="btn btn-primary" href="/events/new">Buat acara baru</a>
		{:else}
			<button class="btn" type="button" onclick={() => load(data.id)}>Coba lagi</button>
		{/if}
	</div>
{:else}
	<p class="hint">Memuat acara…</p>
{/if}

<style>
	.policy {
		margin-top: var(--space-2);
	}

	.link {
		display: block;
		padding: var(--space-3);
		border-radius: var(--radius-md);
		background: var(--paper-sunk);
		font-size: var(--text-base);
		overflow-wrap: anywhere;
	}

	.qr {
		display: block;
		width: min(100%, 17.5rem);
		height: auto;
		margin-inline: auto;
		padding: var(--space-3);
		border-radius: var(--radius-md);
		background: #fff;
		border: 1px solid var(--line);
	}

	.downloads {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: var(--space-3);
	}
</style>
