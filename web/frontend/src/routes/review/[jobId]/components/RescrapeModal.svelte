<script lang="ts">
	import { quintOut } from 'svelte/easing';
	import { fade, scale } from 'svelte/transition';
	import { LoaderCircle, RotateCcw, X, Search, Check } from 'lucide-svelte';
	import { portalToBody } from '$lib/actions/portal';
	import type { Scraper, SearchCandidate } from '$lib/api/types';
	import Button from '$lib/components/ui/Button.svelte';
	import Card from '$lib/components/ui/Card.svelte';
	import ScraperSelector from '$lib/components/ScraperSelector.svelte';

	type ScalarStrategy = '' | 'prefer-nfo' | 'prefer-scraper' | 'preserve-existing' | 'fill-missing-only';

	interface Props {
		show: boolean;
		rescraping: boolean;
		rescrapeMovieId: string;
		bulkMovieCount?: number;
		availableScrapers: Scraper[];
		selectedScrapers: string[];
		manualSearchMode: boolean;
		manualSearchInput: string;
		rescrapePreset?: string;
		rescrapeScalarStrategy: ScalarStrategy;
		onApplyPreset: (preset: 'conservative' | 'gap-fill' | 'aggressive') => void;
		onExecute: (mode: { manualSearchMode: boolean; manualSearchInput: string }) => void;
		onSearchCandidates: (query: string, scrapers: string[]) => Promise<SearchCandidate[]>;
	}

	let {
		show = $bindable(false),
		rescraping,
		rescrapeMovieId,
		bulkMovieCount = undefined,
		availableScrapers,
		selectedScrapers = $bindable([]),
		manualSearchMode = $bindable(false),
		manualSearchInput = $bindable(''),
		rescrapePreset = $bindable(undefined),
		rescrapeScalarStrategy = $bindable('prefer-nfo'),
		onApplyPreset,
		onExecute,
		onSearchCandidates
	}: Props = $props();

	let searchingCandidates = $state(false);
	let candidates = $state<SearchCandidate[]>([]);
	let selectedCandidate = $state<SearchCandidate | null>(null);
	let candidateError = $state('');

	// Reset candidate state when modal closes or mode changes
	$effect(() => {
		if (!show || !manualSearchMode) {
			candidates = [];
			selectedCandidate = null;
			candidateError = '';
			searchingCandidates = false;
		}
	});

	// handleManualSearch is called by the main action button in manual mode.
	// Direct URLs bypass candidate search and rescrape immediately.
	// ID queries fetch candidates first so the user can pick the right match.
	async function handleManualSearch() {
		const query = manualSearchInput.trim();
		if (!query) return;

		// Direct URL → skip candidate search, rescrape immediately
		const isURL = query.startsWith('http://') || query.startsWith('https://');
		if (isURL) {
			onExecute({ manualSearchMode: true, manualSearchInput: query });
			return;
		}

		searchingCandidates = true;
		candidateError = '';
		candidates = [];
		selectedCandidate = null;
		try {
			const results = await onSearchCandidates(query, selectedScrapers);
			if (results.length === 0) {
				candidateError = 'No results found. You can still rescrape using the ID directly.';
			} else if (results.length === 1) {
				// Single result — skip the picker and rescrape immediately
				pickAndRescrape(results[0]);
			} else {
				candidates = results;
			}
		} catch (e: unknown) {
			candidateError = e instanceof Error ? e.message : 'Search failed.';
		} finally {
			searchingCandidates = false;
		}
	}

	function pickAndRescrape(c: SearchCandidate) {
		selectedCandidate = c;
		manualSearchInput = c.detail_url;
		candidates = [];
		onExecute({ manualSearchMode: true, manualSearchInput: c.detail_url });
	}

	function clearCandidateSelection() {
		selectedCandidate = null;
		manualSearchInput = '';
		candidates = [];
		candidateError = '';
	}

	function close() {
		if (rescraping) return;
		show = false;
	}
</script>

{#if show}
	<div
		class="fixed inset-0 bg-black/50 z-50 flex items-center justify-center p-4"
		use:portalToBody
		in:fade|local={{ duration: 140 }}
		out:fade|local={{ duration: 120 }}
	>
		<div
			class="w-full max-w-lg"
			in:scale|local={{ start: 0.97, duration: 180, easing: quintOut }}
			out:scale|local={{ start: 1, opacity: 0.7, duration: 130, easing: quintOut }}
		>
			<Card class="w-full flex flex-col max-h-[90vh]">
				<div class="p-6 border-b flex items-center justify-between">
					<h2 class="text-xl font-bold">
					{#if bulkMovieCount}
						Rescrape {bulkMovieCount} movie{bulkMovieCount !== 1 ? 's' : ''}
					{:else}
						{manualSearchMode ? 'Manual Search' : `Rescrape ${rescrapeMovieId}`}
					{/if}
				</h2>
					<Button variant="ghost" size="icon" onclick={close} disabled={rescraping}>
						{#snippet children()}
							<X class="h-4 w-4" />
						{/snippet}
					</Button>
				</div>

				<div class="flex-1 overflow-auto p-6">
					{#if !bulkMovieCount}
					<div class="flex gap-2 mb-6 p-1 bg-accent rounded-lg">
						<button
							onclick={() => { manualSearchMode = false; clearCandidateSelection(); }}
							class="flex-1 px-4 py-2 rounded transition-all {!manualSearchMode ? 'bg-card shadow-sm font-medium' : 'text-muted-foreground hover:text-foreground'}"
						>
							Rescrape from File
						</button>
						<button
							onclick={() => (manualSearchMode = true)}
							class="flex-1 px-4 py-2 rounded transition-all {manualSearchMode ? 'bg-card shadow-sm font-medium' : 'text-muted-foreground hover:text-foreground'}"
						>
							Manual Search
						</button>
					</div>
					{/if}

					{#if manualSearchMode}
						<div class="space-y-4">
							{#if selectedCandidate}
								<!-- Confirmed selection chip -->
								<div class="flex items-center gap-2 p-3 rounded-lg border border-primary/40 bg-primary/5">
									{#if selectedCandidate.cover_url}
										<img src={selectedCandidate.cover_url} alt={selectedCandidate.id} class="h-12 w-9 object-cover rounded" />
									{/if}
									<div class="flex-1 min-w-0">
										<p class="font-medium text-sm truncate">{selectedCandidate.id}</p>
										{#if selectedCandidate.title}
											<p class="text-xs text-muted-foreground truncate">{selectedCandidate.title}</p>
										{/if}
										<p class="text-xs text-muted-foreground">via {selectedCandidate.source}</p>
									</div>
									<Check class="h-4 w-4 text-primary shrink-0" />
									<button onclick={clearCandidateSelection} class="text-xs text-muted-foreground hover:text-foreground underline shrink-0">
										Change
									</button>
								</div>
							{:else if candidates.length > 0}
								<!-- Candidate picker grid -->
								<div>
									<p class="text-sm font-medium mb-2">{candidates.length} results found — pick one:</p>
									<div class="grid grid-cols-3 gap-2 max-h-56 overflow-y-auto pr-1">
										{#each candidates as candidate (candidate.detail_url)}
											<button
												onclick={() => pickAndRescrape(candidate)}
												class="flex flex-col items-center gap-1 p-2 rounded-lg border hover:border-primary hover:bg-primary/5 transition-all text-left"
											>
												{#if candidate.cover_url}
													<img
														src={candidate.cover_url}
														alt={candidate.id}
														class="w-full aspect-[2/3] object-cover rounded"
													/>
												{:else}
													<div class="w-full aspect-[2/3] bg-muted rounded flex items-center justify-center">
														<span class="text-xs text-muted-foreground">No image</span>
													</div>
												{/if}
												<p class="text-xs font-mono font-medium w-full truncate text-center">{candidate.id}</p>
												{#if candidate.title}
													<p class="text-xs text-muted-foreground w-full truncate text-center">{candidate.title}</p>
												{/if}
											</button>
										{/each}
									</div>
								</div>
							{:else}
								<!-- Normal search input -->
								<div>
									<label for="manual-search-input" class="text-sm font-medium mb-2 block">
										DVD ID, Content ID, or Direct URL
									</label>
									<input
										id="manual-search-input"
										type="text"
										bind:value={manualSearchInput}
										placeholder="e.g., IPX-123 or https://www.dmm.co.jp/..."
										class="w-full px-3 py-2 border rounded-md bg-background focus:ring-2 focus:ring-primary focus:border-primary transition-all font-mono text-sm"
										onkeydown={(e) => { if (e.key === 'Enter') handleManualSearch(); }}
									/>
									{#if candidateError}
										<p class="text-xs text-destructive mt-2">{candidateError}</p>
									{:else}
										<p class="text-xs text-muted-foreground mt-2">
											Enter a DVD ID to search for matching results, or paste a direct URL to skip search.
										</p>
									{/if}
								</div>

								<div>
									<p class="text-sm text-muted-foreground mb-4">
										Select which scrapers to use. The results will be aggregated according to your configured priorities.
									</p>

									<ScraperSelector
										scrapers={availableScrapers}
										bind:selected={selectedScrapers}
										disabled={false}
									/>
								</div>
							{/if}
						</div>
					{:else}
						<p class="text-sm text-muted-foreground mb-4">
							Select which scrapers to use for fetching fresh metadata. The results will be
							aggregated according to your configured priorities.
						</p>

						<ScraperSelector
							scrapers={availableScrapers}
							bind:selected={selectedScrapers}
							disabled={false}
						/>
					{/if}

					{#if !candidates.length && !searchingCandidates}
					<div class="mt-6 space-y-4">
						<div>
							<h3 class="font-semibold mb-2">NFO Merge Strategy</h3>
							<p class="text-sm text-muted-foreground mb-3">
								Choose how to merge existing NFO data with freshly scraped data. Leave empty to replace all data.
							</p>
						</div>

						<div class="space-y-2">
							<div class="flex items-center justify-between">
								<h4 class="text-sm font-medium">Quick Presets</h4>
								{#if rescrapePreset}
									<button onclick={() => (rescrapePreset = undefined)} class="text-xs text-primary hover:underline">
										Clear preset
									</button>
								{/if}
							</div>
							<div class="grid grid-cols-3 gap-2">
								<button
									onclick={() => onApplyPreset('conservative')}
									class="p-3 rounded-lg border-2 text-sm transition-all {rescrapePreset === 'conservative' ? 'border-primary bg-primary/5 font-medium' : 'border-border hover:border-primary/50'}"
								>
									<div class="font-medium">🛡️ Conservative</div>
									<div class="text-xs text-muted-foreground mt-1">Never overwrite existing</div>
								</button>
								<button
									onclick={() => onApplyPreset('gap-fill')}
									class="p-3 rounded-lg border-2 text-sm transition-all {rescrapePreset === 'gap-fill' ? 'border-primary bg-primary/5 font-medium' : 'border-border hover:border-primary/50'}"
								>
									<div class="font-medium">📝 Gap Fill</div>
									<div class="text-xs text-muted-foreground mt-1">Fill missing fields only</div>
								</button>
								<button
									onclick={() => onApplyPreset('aggressive')}
									class="p-3 rounded-lg border-2 text-sm transition-all {rescrapePreset === 'aggressive' ? 'border-primary bg-primary/5 font-medium' : 'border-border hover:border-primary/50'}"
								>
									<div class="font-medium">⚡ Aggressive</div>
									<div class="text-xs text-muted-foreground mt-1">Trust scrapers completely</div>
								</button>
							</div>
						</div>

						<div class="space-y-2">
							<h4 class="text-sm font-medium">Or Choose Individual Strategies</h4>
							<div class="grid grid-cols-2 gap-2">
								<button
									onclick={() => {
										rescrapeScalarStrategy = 'prefer-nfo';
										rescrapePreset = undefined;
									}}
									class="p-3 rounded-lg border-2 text-sm transition-all {rescrapeScalarStrategy === 'prefer-nfo' ? 'border-primary bg-primary/5 font-medium' : 'border-border hover:border-primary/50'}"
								>
									<div class="font-medium">Prefer NFO</div>
									<div class="text-xs text-muted-foreground mt-1">Keep existing data</div>
								</button>
								<button
									onclick={() => {
										rescrapeScalarStrategy = 'prefer-scraper';
										rescrapePreset = undefined;
									}}
									class="p-3 rounded-lg border-2 text-sm transition-all {rescrapeScalarStrategy === 'prefer-scraper' ? 'border-primary bg-primary/5 font-medium' : 'border-border hover:border-primary/50'}"
								>
									<div class="font-medium">Prefer Scraped</div>
									<div class="text-xs text-muted-foreground mt-1">Update with fresh data</div>
								</button>
								<button
									onclick={() => {
										rescrapeScalarStrategy = 'preserve-existing';
										rescrapePreset = undefined;
									}}
									class="p-3 rounded-lg border-2 text-sm transition-all {rescrapeScalarStrategy === 'preserve-existing' ? 'border-primary bg-primary/5 font-medium' : 'border-border hover:border-primary/50'}"
								>
									<div class="font-medium">Preserve Existing</div>
									<div class="text-xs text-muted-foreground mt-1">Never overwrite</div>
								</button>
								<button
									onclick={() => {
										rescrapeScalarStrategy = 'fill-missing-only';
										rescrapePreset = undefined;
									}}
									class="p-3 rounded-lg border-2 text-sm transition-all {rescrapeScalarStrategy === 'fill-missing-only' ? 'border-primary bg-primary/5 font-medium' : 'border-border hover:border-primary/50'}"
								>
									<div class="font-medium">Fill Missing Only</div>
									<div class="text-xs text-muted-foreground mt-1">Safe gap filling</div>
								</button>
								<button
									onclick={() => {
										rescrapeScalarStrategy = '';
										rescrapePreset = undefined;
									}}
									class="p-3 rounded-lg border-2 text-sm transition-all col-span-2 {rescrapeScalarStrategy === '' ? 'border-primary bg-primary/5 font-medium' : 'border-border hover:border-primary/50'}"
								>
									<div class="font-medium">Replace All</div>
									<div class="text-xs text-muted-foreground mt-1">Fresh scrape only (ignore existing NFO)</div>
								</button>
							</div>
						</div>
					</div>
					{/if}
				</div>

				<div class="p-6 border-t flex items-center justify-end gap-3">
					<Button variant="outline" onclick={close} disabled={rescraping}>
						{#snippet children()}Cancel{/snippet}
					</Button>
					{#if candidates.length > 0}
						<Button variant="outline" onclick={clearCandidateSelection}>
							{#snippet children()}Back{/snippet}
						</Button>
					{:else}
						<Button
							onclick={manualSearchMode && !bulkMovieCount
								? handleManualSearch
								: () => onExecute({ manualSearchMode, manualSearchInput })}
							disabled={rescraping || searchingCandidates || (manualSearchMode && !manualSearchInput.trim())}
						>
							{#snippet children()}
								{#if rescraping || searchingCandidates}
									<LoaderCircle class="h-4 w-4 mr-2 animate-spin" />
									{searchingCandidates ? 'Searching...' : (bulkMovieCount ? `Rescraping ${bulkMovieCount} movies...` : 'Scraping...')}
								{:else if manualSearchMode && !bulkMovieCount}
									<Search class="h-4 w-4 mr-2" />
									Search
								{:else}
									<RotateCcw class="h-4 w-4 mr-2" />
									{bulkMovieCount ? `Rescrape ${bulkMovieCount} movies` : 'Rescrape'}
								{/if}
							{/snippet}
						</Button>
					{/if}
				</div>
			</Card>
		</div>
	</div>
{/if}
