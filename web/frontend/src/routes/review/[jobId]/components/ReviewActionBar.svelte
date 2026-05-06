<script lang="ts">
	import Button from '$lib/components/ui/Button.svelte';
	import Card from '$lib/components/ui/Card.svelte';
	import { LoaderCircle, Play, X } from 'lucide-svelte';

	interface Props {
		isUpdateMode: boolean;
		organizing: boolean;
		destinationPath: string;
		movieResultsLength: number;
		selectedCount: number;
		onCancel: () => void;
		onOrganizeAll: () => void;
		onOrganizeSelected: () => void;
		onOrganizeCurrent: () => void;
	}

	let {
		isUpdateMode,
		organizing,
		destinationPath,
		movieResultsLength,
		selectedCount,
		onCancel,
		onOrganizeAll,
		onOrganizeSelected,
		onOrganizeCurrent
	}: Props = $props();
</script>

{#if !isUpdateMode}
	<Card class="p-4">
		<div class="flex items-center justify-end gap-3">
			<Button variant="outline" onclick={onCancel} disabled={organizing}>
				{#snippet children()}
					<X class="h-4 w-4 mr-2" />
					Cancel
				{/snippet}
			</Button>
			<Button variant="outline" onclick={onOrganizeCurrent} disabled={organizing || !destinationPath.trim()}>
				{#snippet children()}
					{#if organizing}
						<LoaderCircle class="h-4 w-4 mr-2 animate-spin" />
					{:else}
						<Play class="h-4 w-4 mr-2" />
					{/if}
					Organize This Movie
				{/snippet}
			</Button>
			{#if selectedCount > 0}
				<Button variant="outline" onclick={onOrganizeSelected} disabled={organizing || !destinationPath.trim()}>
					{#snippet children()}
						{#if organizing}
							<LoaderCircle class="h-4 w-4 mr-2 animate-spin" />
						{:else}
							<Play class="h-4 w-4 mr-2" />
						{/if}
						Organize Selected ({selectedCount})
					{/snippet}
				</Button>
			{/if}
			<Button onclick={onOrganizeAll} disabled={organizing || !destinationPath.trim()}>
				{#snippet children()}
					{#if organizing}
						<LoaderCircle class="h-4 w-4 mr-2 animate-spin" />
					{:else}
						<Play class="h-4 w-4 mr-2" />
					{/if}
					{organizing ? 'Organizing...' : `Organize ${movieResultsLength} File${movieResultsLength !== 1 ? 's' : ''}`}
				{/snippet}
			</Button>
		</div>
	</Card>
{/if}
