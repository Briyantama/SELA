<script lang="ts">
	interface Props {
		/** What ends up on the clipboard. */
		text: string;
		label: string;
		done: string;
		failed: string;
	}

	let { text, label, done, failed }: Props = $props();

	let outcome = $state<'idle' | 'done' | 'failed'>('idle');

	async function copy() {
		try {
			// Absent on insecure origins and in some in-app browsers.
			if (!navigator.clipboard) throw new Error('clipboard unavailable');
			await navigator.clipboard.writeText(text);
			outcome = 'done';
		} catch {
			outcome = 'failed';
		}
	}
</script>

<button class="btn" type="button" onclick={copy}>{label}</button>
<!-- Always rendered so screen readers announce the change when it fills in. -->
<p class="feedback" class:bad={outcome === 'failed'} role="status">
	{outcome === 'done' ? done : outcome === 'failed' ? failed : ''}
</p>

<style>
	.feedback {
		min-height: 1.5em;
		font-size: var(--text-sm);
		color: var(--ok);
	}

	.feedback.bad {
		color: var(--danger);
	}
</style>
