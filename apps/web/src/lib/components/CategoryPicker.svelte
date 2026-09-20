<script lang="ts">
	import type { Category } from '$lib/api/events';
	import { describeDefaults } from '$lib/events/presets';

	interface Props {
		categories: Category[];
		/** Code of the chosen category, or '' when none is chosen. */
		value: string;
	}

	let { categories, value = $bindable() }: Props = $props();
</script>

<fieldset class="picker">
	<legend>Pilih jenis acara</legend>
	{#each categories as category (category.code)}
		<label class="option">
			<input type="radio" name="category" value={category.code} bind:group={value} />
			<span class="name">{category.name}</span>
			<span class="defaults">{describeDefaults(category)}</span>
		</label>
	{/each}
</fieldset>

<style>
	.picker {
		display: grid;
		gap: var(--space-3);
		margin: 0;
		padding: 0;
		border: 0;
		min-width: 0;
	}

	legend {
		font-weight: 600;
		margin-bottom: var(--space-3);
		padding: 0;
	}

	.option {
		position: relative;
		display: grid;
		gap: var(--space-1);
		min-height: var(--tap-target);
		padding: var(--space-3) var(--space-4) var(--space-3) 3rem;
		border: 1.5px solid var(--line);
		border-radius: var(--radius-md);
		background: var(--paper-raised);
		cursor: pointer;
		transition:
			border-color var(--duration-fast) var(--ease-out),
			transform var(--duration-fast) var(--ease-out);
	}

	.option:hover {
		border-color: var(--ink-soft);
	}

	/* The native radio stays in the tab order and the accessibility tree; it is drawn as a ring. */
	.option input {
		position: absolute;
		left: var(--space-4);
		top: 50%;
		width: 1.25rem;
		min-height: 0;
		height: 1.25rem;
		margin: 0;
		transform: translateY(-50%);
		accent-color: var(--accent);
	}

	.option:has(input:checked) {
		border-color: var(--accent);
		background: #fff3e6;
		box-shadow: inset 4px 0 0 var(--accent);
	}

	.option:has(input:focus-visible) {
		outline: 3px solid var(--focus);
		outline-offset: 2px;
	}

	.name {
		font-family: var(--font-display);
		font-size: var(--text-lg);
		font-weight: 600;
	}

	.defaults {
		color: var(--ink-soft);
		font-size: var(--text-sm);
	}
</style>
