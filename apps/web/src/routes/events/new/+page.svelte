<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { ApiError } from '$lib/api/envelope';
	import { createEvent, listCategories, type Category, type EventView } from '$lib/api/events';
	import CategoryPicker from '$lib/components/CategoryPicker.svelte';
	import {
		buildCreateBody,
		validateEventForm,
		type EventFormErrors,
		type EventFormValues
	} from '$lib/events/form';
	import { describeDefaults } from '$lib/events/presets';
	import { describeEventError } from '$lib/i18n/errors';

	const NEW_EVENT_PATH = '/events/new';

	let categories = $state<Category[]>();
	let loadError = $state('');
	let step = $state<'category' | 'details'>('category');
	let selected = $state('');

	let values = $state<EventFormValues>({
		name: '',
		eventDate: '',
		shotLimit: '',
		revealMode: '',
		revealDelayHours: ''
	});
	let errors = $state<EventFormErrors>({});
	let submitError = $state('');
	let advancedOpen = $state(false);
	let busy = $state(false);
	/** Set once the event exists, so the form cannot create a second one. */
	let createdId = $state('');
	let detailsFor = '';

	const category = $derived(categories?.find((c) => c.code === selected));
	const effectiveMode = $derived(values.revealMode || category?.effective_reveal_mode);

	// The delay field is hidden unless the reveal is delayed; a value typed earlier must not linger
	// unseen (it would fail validation or be sent with an instant reveal).
	$effect(() => {
		if (effectiveMode !== 'delayed' && values.revealDelayHours !== '') values.revealDelayHours = '';
	});

	function chooseCategory() {
		// Overrides are tuned to one category's defaults; name and date carry over.
		if (selected !== detailsFor) {
			values = { ...values, shotLimit: '', revealMode: '', revealDelayHours: '' };
			errors = {};
			advancedOpen = false;
			detailsFor = selected;
		}
		step = 'details';
	}

	async function loadCategories() {
		loadError = '';
		try {
			categories = await listCategories();
		} catch (err) {
			loadError = describeEventError(err);
		}
	}

	onMount(loadCategories);

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		if (busy || createdId || !category) return;

		submitError = '';
		errors = validateEventForm(values, category);
		if (Object.keys(errors).length > 0) {
			// Never leave an error inside a collapsed section.
			if (errors.shotLimit || errors.revealDelayHours) advancedOpen = true;
			return;
		}

		busy = true;
		let created: EventView;
		try {
			created = await createEvent(buildCreateBody(category.code, values));
		} catch (err) {
			busy = false;
			if (err instanceof ApiError && err.status === 401) {
				await goto(`/auth/login?next=${encodeURIComponent(NEW_EVENT_PATH)}`);
				return;
			}
			submitError = describeEventError(err);
			return;
		}

		// From here the event exists. Opening it is a separate step: if it fails, the host gets a
		// link instead of a form that would create a duplicate.
		createdId = created.event_id;
		busy = false;
		try {
			await goto(`/events/${encodeURIComponent(created.event_id)}`);
		} catch {
			// The "Buka acara" link below is the fallback.
		}
	}
</script>

<svelte:head>
	<title>Buat acara · Sela</title>
</svelte:head>

<div class="stack">
	<header>
		<p class="eyebrow">Langkah {step === 'category' ? 1 : 2} dari 2</p>
		<h1>Buat acara baru</h1>
		<p class="lede">Pilih jenis acara, lalu isi nama dan tanggalnya. Sisanya bisa menyusul.</p>
	</header>

	{#if step === 'category'}
		<section class="card stack">
			{#if loadError}
				<p class="alert" role="alert">{loadError}</p>
				<button class="btn" type="button" onclick={loadCategories}>Coba lagi</button>
			{:else if !categories}
				<p class="hint">Memuat jenis acara…</p>
			{:else}
				<CategoryPicker {categories} bind:value={selected} />
				<button
					class="btn btn-primary"
					type="button"
					disabled={!category}
					onclick={chooseCategory}>Lanjut</button
				>
			{/if}
		</section>
	{:else if category}
		<form class="card stack" novalidate onsubmit={submit}>
			<p class="chosen">
				Jenis acara: <strong>{category.name}</strong>
				<span class="hint">{describeDefaults(category)}</span>
			</p>

			<div class="field">
				<label for="name">Nama acara</label>
				<input
					id="name"
					autocomplete="off"
					placeholder="mis. Pernikahan Sari & Budi"
					bind:value={values.name}
					aria-invalid={errors.name ? 'true' : undefined}
					aria-describedby={errors.name ? 'name-error' : undefined}
				/>
				{#if errors.name}<p class="field-error" id="name-error">{errors.name}</p>{/if}
			</div>

			<div class="field">
				<label for="event-date">Tanggal acara</label>
				<input
					id="event-date"
					type="date"
					bind:value={values.eventDate}
					aria-invalid={errors.eventDate ? 'true' : undefined}
					aria-describedby={errors.eventDate ? 'event-date-error' : undefined}
				/>
				{#if errors.eventDate}<p class="field-error" id="event-date-error">{errors.eventDate}</p>{/if}
			</div>

			<div class="field">
				<label for="timezone">Zona waktu</label>
				<input id="timezone" value="WIB (Asia/Jakarta)" readonly />
				<p class="hint">Semua acara memakai WIB untuk saat ini.</p>
			</div>

			<details bind:open={advancedOpen}>
				<summary>Pengaturan lanjutan</summary>
				<div class="stack advanced">
					<p class="hint">Kosongkan untuk mengikuti preset jenis acara.</p>

					<div class="field">
						<label for="shot-limit">Batas jepretan per tamu</label>
						<input
							id="shot-limit"
							inputmode="numeric"
							placeholder="Ikuti preset"
							bind:value={values.shotLimit}
							aria-invalid={errors.shotLimit ? 'true' : undefined}
							aria-describedby={errors.shotLimit ? 'shot-limit-error' : undefined}
						/>
						<p class="hint">0 berarti tak terbatas.</p>
						{#if errors.shotLimit}<p class="field-error" id="shot-limit-error">{errors.shotLimit}</p>{/if}
					</div>

					<div class="field">
						<label for="reveal-mode">Mode reveal</label>
						<select id="reveal-mode" bind:value={values.revealMode}>
							<option value="">
								Ikuti preset ({category.effective_reveal_mode === 'delayed' ? 'Tertunda' : 'Langsung'})
							</option>
							<option value="instant">Langsung</option>
							<option value="delayed">Tertunda</option>
						</select>
					</div>

					{#if effectiveMode === 'delayed'}
						<div class="field">
							<label for="reveal-delay">Jeda reveal (jam)</label>
							<input
								id="reveal-delay"
								inputmode="numeric"
								placeholder={category.effective_reveal_delay_hours === null
									? 'mis. 24'
									: `Preset: ${category.effective_reveal_delay_hours}`}
								bind:value={values.revealDelayHours}
								aria-invalid={errors.revealDelayHours ? 'true' : undefined}
								aria-describedby={errors.revealDelayHours ? 'reveal-delay-error' : undefined}
							/>
							{#if errors.revealDelayHours}
								<p class="field-error" id="reveal-delay-error">{errors.revealDelayHours}</p>
							{/if}
						</div>
					{/if}
				</div>
			</details>

			{#if submitError}<p class="alert" role="alert">{submitError}</p>{/if}
			{#if createdId}
				<p class="notice">
					Acara sudah dibuat. <a href={`/events/${encodeURIComponent(createdId)}`}>Buka acara</a>
				</p>
			{/if}

			<div class="actions">
				<button class="btn btn-primary" type="submit" disabled={busy || createdId !== ''}
					>Buat acara</button
				>
				<button class="btn btn-quiet" type="button" onclick={() => (step = 'category')}>Kembali</button>
			</div>
		</form>
	{/if}
</div>

<style>
	.chosen {
		display: grid;
		gap: var(--space-1);
	}

	details {
		border-top: 1px solid var(--line);
		padding-top: var(--space-3);
	}

	summary {
		min-height: var(--tap-target);
		display: flex;
		align-items: center;
		font-weight: 600;
		cursor: pointer;
	}

	.advanced {
		padding-top: var(--space-3);
	}

	.actions {
		display: grid;
		gap: var(--space-2);
	}
</style>
