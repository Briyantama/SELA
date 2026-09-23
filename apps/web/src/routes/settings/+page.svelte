<script lang="ts">
	import { onMount } from 'svelte';
	import { invalidateAll } from '$app/navigation';
	import { ApiError } from '$lib/api/envelope';
	import { getProfile, updatePreferences, type Profile, type Theme } from '$lib/api/rbac';

	// This page's own chrome is the one place besides the nav that really switches language; the
	// three older host pages stay Indonesian-only (a separate, larger retrofit).
	const STRINGS = {
		id: {
			title: 'Pengaturan',
			role: 'Peran',
			theme: 'Tema',
			light: 'Terang',
			dark: 'Gelap',
			language: 'Bahasa',
			loading: 'Memuat pengaturan…',
			error: 'Gagal memuat pengaturan. Coba lagi.'
		},
		en: {
			title: 'Settings',
			role: 'Role',
			theme: 'Theme',
			light: 'Light',
			dark: 'Dark',
			language: 'Language',
			loading: 'Loading settings…',
			error: 'Could not load settings. Try again.'
		}
	} as const;

	let profile = $state<Profile>();
	let error = $state('');
	let saving = $state(false);

	const lang = $derived(profile?.preferences.language === 'en' ? 'en' : 'id');
	const t = $derived(STRINGS[lang]);

	async function load() {
		error = '';
		try {
			profile = await getProfile();
		} catch {
			error = STRINGS.id.error;
		}
	}

	onMount(load);

	async function apply(input: { theme?: Theme; language?: string }) {
		if (saving || !profile) return;
		if (input.theme === profile.preferences.theme || input.language === profile.preferences.language) return;
		saving = true;
		error = '';
		try {
			profile = await updatePreferences(input);
			document.documentElement.dataset.theme = profile.preferences.theme;
			// The nav and every other page read the profile through the root layout's load(); refresh
			// it so a language switch relabels the nav too.
			await invalidateAll();
		} catch (err) {
			error = err instanceof ApiError ? err.message : t.error;
		} finally {
			saving = false;
		}
	}
</script>

<svelte:head>
	<title>{profile ? t.title : 'Sela'}</title>
</svelte:head>

{#if profile}
	<div class="stack">
		<header>
			<p class="eyebrow">Sela</p>
			<h1>{t.title}</h1>
			<p class="lede">{t.role}: {profile.active_role.name}</p>
		</header>

		<section class="card stack">
			<h2>{t.theme}</h2>
			<div class="toggle-group" role="group" aria-label={t.theme}>
				<button
					type="button"
					class="btn toggle"
					aria-pressed={profile.preferences.theme === 'light'}
					disabled={saving}
					onclick={() => apply({ theme: 'light' })}>{t.light}</button
				>
				<button
					type="button"
					class="btn toggle"
					aria-pressed={profile.preferences.theme === 'dark'}
					disabled={saving}
					onclick={() => apply({ theme: 'dark' })}>{t.dark}</button
				>
			</div>
		</section>

		<section class="card stack">
			<h2>{t.language}</h2>
			<div class="toggle-group" role="group" aria-label={t.language}>
				{#each profile.preferences.supported_languages as option (option.code)}
					<button
						type="button"
						class="btn toggle"
						aria-pressed={profile.preferences.language === option.code}
						disabled={saving}
						onclick={() => apply({ language: option.code })}>{option.name}</button
					>
				{/each}
			</div>
		</section>

		{#if error}<p class="alert" role="alert">{error}</p>{/if}
	</div>
{:else if error}
	<p class="alert" role="alert">{error}</p>
{:else}
	<p class="hint">{STRINGS.id.loading}</p>
{/if}

<style>
	.toggle-group {
		display: flex;
		gap: var(--space-2);
		flex-wrap: wrap;
	}

	.toggle[aria-pressed='true'] {
		background: var(--accent);
		border-color: var(--accent-strong);
		color: var(--accent-ink);
	}
</style>
