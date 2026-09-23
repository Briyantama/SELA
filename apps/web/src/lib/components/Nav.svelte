<script lang="ts">
	import { page } from '$app/state';
	import type { MenuNode } from '$lib/api/rbac';

	interface Props {
		menus: MenuNode[];
	}

	let { menus }: Props = $props();

	// Only top-level link menus are rendered: the seed is flat, and the schema's group/nesting
	// support is exercised elsewhere (the backend RBAC tests), not yet by any real menu here.
	const links = $derived(menus.filter((m): m is MenuNode & { path: string } => m.type === 'link' && !!m.path));
</script>

{#if links.length > 0}
	<nav class="nav" aria-label="Navigasi utama">
		{#each links as menu (menu.code)}
			<a class="nav-link" href={menu.path} aria-current={page.url.pathname === menu.path ? 'page' : undefined}>
				{menu.label}
			</a>
		{/each}
	</nav>
{/if}

<style>
	.nav {
		display: flex;
		gap: var(--space-2);
		padding: var(--space-3) var(--space-4);
		border-bottom: 1px solid var(--line);
	}

	.nav-link {
		display: flex;
		align-items: center;
		min-height: var(--tap-target);
		padding: 0 var(--space-3);
		border-radius: var(--radius-md);
		color: var(--ink-soft);
		font-weight: 600;
		text-decoration: none;
		transition: background-color var(--duration-fast) var(--ease-out), color var(--duration-fast) var(--ease-out);
	}

	.nav-link:hover {
		background: var(--paper-sunk);
	}

	.nav-link[aria-current='page'] {
		color: var(--accent-strong);
		background: var(--paper-raised);
	}
</style>
