<script lang="ts">
	import favicon from '$lib/assets/favicon.svg';
	import '$lib/styles/tokens.css';
	import '$lib/styles/base.css';
	import Nav from '$lib/components/Nav.svelte';
	import type { LayoutProps } from './$types';

	let { data, children }: LayoutProps = $props();

	// The nav and the theme both come from the same best-effort profile fetch (+layout.ts); a
	// signed-out host (e.g. on /auth/login) gets no nav and the light default, never an error here.
	$effect(() => {
		document.documentElement.dataset.theme = data.profile?.preferences.theme ?? 'light';
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
	<meta name="robots" content="noindex" />
</svelte:head>

{#if data.profile}
	<Nav menus={data.profile.menus} />
{/if}

<main class="shell">
	{@render children()}
</main>
