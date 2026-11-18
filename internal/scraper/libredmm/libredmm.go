package libredmm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/javinizer/javinizer-go/internal/config"
	"github.com/javinizer/javinizer-go/internal/httpclient"
	"github.com/javinizer/javinizer-go/internal/imageutil"
	"github.com/javinizer/javinizer-go/internal/logging"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/javinizer/javinizer-go/internal/ratelimit"
	"github.com/javinizer/javinizer-go/internal/scraper/image/placeholder"
	"github.com/javinizer/javinizer-go/internal/scraperutil"
)

const (
	defaultBaseURL      = "https://www.libredmm.com"
	defaultPollAttempts = 6
	defaultPollInterval = 1500 * time.Millisecond
	// maxPollInterval caps the server-provided Retry-After delay between polls
	// of a movie LibreDMM is still fetching (202 Accepted).
	maxPollInterval = 5 * time.Second
	// movieAPIPath is LibreDMM's JSON lookup endpoint: GET {base}/api/v1/movies/{code}.
	// {code} may be a display code (IPX-123), a DMM content ID (ipx00123) or a
	// pseudo code (SW-001~45262) that selects one release of an ambiguous code.
	movieAPIPath = "/api/v1/movies/"
)

var (
	cidRegex                 = regexp.MustCompile(`(?i)(?:^|[?&])(cid|id)=([^&]+)`)
	dmmPrefixedCIDRegex      = regexp.MustCompile(`^(\d{3,}[a-z]+)0+(\d+.*)$`)
	dmmSampleFilenamePattern = regexp.MustCompile(`(?i)\.jpe?g$`)
	// URL extraction pattern (matches /movies/{id} and /api/v1/movies/{id})
	libreDMPathRegex = regexp.MustCompile(`/movies/([^/?&]+)`)
	// ANSI escape sequence pattern for stripping terminal color codes
	ansiEscapeRegex = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
	// Pattern to strip bare ESC character (0x1b) that may be injected by proxies
	// This handles cases where ESC appears without the bracket `[`
	escCharRegex = regexp.MustCompile("\x1b")
	// Pattern to strip other control characters (0x00-0x1F) except tab, newline, carriage return
	// These can be injected by broken proxies or terminal wrappers
	controlCharRegex = regexp.MustCompile("[\x00-\x08\x0b\x0c\x0e-\x1f]")
)

// namedEntity is the {id, name} shape LibreDMM uses for makers, labels,
// series, directors and genres.
type namedEntity struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type actressPayload struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Ruby     string `json:"ruby"`
	ImageURL string `json:"image_url"`
}

type coverPayload struct {
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url"`
}

type reviewPayload struct {
	Average float64 `json:"average"`
	Count   int     `json:"count"`
}

type productPayload struct {
	ContentID string `json:"content_id"`
	Service   string `json:"service"`
	Floor     string `json:"floor"`
	URL       string `json:"url"`
	Preferred bool   `json:"preferred"`
}

// moviePayload is the "movie" object of a LibreDMM /api/v1/movies/{code} response.
type moviePayload struct {
	Code           string           `json:"code"`
	BaseCode       string           `json:"base_code"`
	PseudoCode     string           `json:"pseudo_code"`
	Title          string           `json:"title"`
	ReleaseDate    string           `json:"release_date"`
	RuntimeMinutes int              `json:"runtime_minutes"`
	Description    string           `json:"description"`
	Cover          coverPayload     `json:"cover"`
	SampleImages   []string         `json:"sample_images"`
	Review         *reviewPayload   `json:"review"`
	Actresses      []actressPayload `json:"actresses"`
	Makers         []namedEntity    `json:"makers"`
	Labels         []namedEntity    `json:"labels"`
	Series         []namedEntity    `json:"series"`
	Directors      []namedEntity    `json:"directors"`
	Genres         []namedEntity    `json:"genres"`
	Products       []productPayload `json:"products"`
}

// workPayload is one release in an ambiguous-code response. Its Code is a
// pseudo code (e.g. "SW-001~45262") that can be looked up on its own.
type workPayload struct {
	Code        string `json:"code"`
	BaseCode    string `json:"base_code"`
	Maker       string `json:"maker"`
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date"`
	ThumbURL    string `json:"thumb_url"`
	CoverURL    string `json:"cover_url"`
}

// apiResponse covers every body shape the movies endpoint returns:
//   - 200 {"movie": {...}}                    — single match
//   - 200 {"code": "...", "works": [...]}     — code shared by several releases
//   - 202 null                                — lookup in progress, retry later
//   - 4xx/5xx application/problem+json        — {"title", "status", "detail"}
type apiResponse struct {
	Movie *moviePayload `json:"movie"`
	Code  string        `json:"code"`
	Works []workPayload `json:"works"`

	// RFC 7807 problem fields (error responses only)
	ProblemTitle string `json:"title"`
	Detail       string `json:"detail"`
}

func (r *apiResponse) problem() string {
	if r == nil {
		return ""
	}
	if msg := scraperutil.CleanString(r.Detail); msg != "" {
		return msg
	}
	return scraperutil.CleanString(r.ProblemTitle)
}

// Scraper implements the LibreDMM scraper.
type Scraper struct {
	client          *resty.Client
	enabled         bool
	baseURL         string
	proxyOverride   *config.ProxyConfig
	downloadProxy   *config.ProxyConfig
	rateLimiter     *ratelimit.Limiter
	pollInterval    time.Duration
	maxPollAttempts int
	settings        config.ScraperSettings // stores the full settings for Config() method
}

// New creates a new LibreDMM scraper.
func New(settings config.ScraperSettings, globalProxy *config.ProxyConfig, globalFlareSolverr config.FlareSolverrConfig) *Scraper {
	result := httpclient.InitScraperClient(&settings, globalProxy, globalFlareSolverr,
		httpclient.WithScraperHeaders(httpclient.CombineHeaders(
			httpclient.JSONAPIHeaders(),
			httpclient.UserAgentHeader(settings.UserAgent),
			map[string]string{"Accept-Language": "ja,en-US;q=0.8,en;q=0.6"},
		)),
	)
	client := result.Client

	base := strings.TrimSpace(settings.BaseURL)
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	s := &Scraper{
		client:          client,
		enabled:         settings.Enabled,
		baseURL:         base,
		proxyOverride:   settings.Proxy,
		downloadProxy:   settings.DownloadProxy,
		rateLimiter:     ratelimit.NewLimiter(time.Duration(settings.RateLimit) * time.Millisecond),
		pollInterval:    defaultPollInterval,
		maxPollAttempts: defaultPollAttempts,
		settings:        settings,
	}

	if result.ProxyEnabled && strings.TrimSpace(result.ProxyProfile.URL) != "" {
		logging.Infof("LibreDMM: Using proxy %s", httpclient.SanitizeProxyURL(result.ProxyProfile.URL))
	}

	return s
}

// Name returns scraper identifier.
func (s *Scraper) Name() string { return "libredmm" }

// IsEnabled returns whether scraper is enabled.
func (s *Scraper) IsEnabled() bool { return s.enabled }

// Config returns the scraper's configuration
func (s *Scraper) Config() *config.ScraperSettings {
	return s.settings.DeepCopy()
}

// Close cleans up resources held by the scraper
func (s *Scraper) Close() error {
	return nil
}

// CanHandleURL returns true if this scraper can handle the given URL
func (s *Scraper) CanHandleURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "libredmm.com" || strings.HasSuffix(host, ".libredmm.com")
}

// ExtractIDFromURL extracts the movie ID from a LibreDMM URL
func (s *Scraper) ExtractIDFromURL(urlStr string) (string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}

	// Check for search query parameter
	if q := strings.TrimSpace(u.Query().Get("q")); q != "" {
		return q, nil
	}

	// Extract from /movies/{id}, /movies/{id}.json, /api/v1/movies/{id}
	matches := libreDMPathRegex.FindStringSubmatch(u.Path)
	if len(matches) > 1 {
		id, _ := url.PathUnescape(matches[1])
		return strings.TrimSuffix(id, ".json"), nil
	}

	// Check for /cid/{id} format (case-insensitive)
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 2 && strings.EqualFold(parts[0], "cid") {
		id, _ := url.PathUnescape(parts[1])
		id = strings.TrimSuffix(id, ".json")
		if id != "" {
			return id, nil
		}
	}

	return "", fmt.Errorf("failed to extract ID from LibreDMM URL")
}

// ScrapeURL scrapes a LibreDMM movie page URL (or API URL) directly.
func (s *Scraper) ScrapeURL(ctx context.Context, urlStr string) (*models.ScraperResult, error) {
	if !s.CanHandleURL(urlStr) {
		return nil, models.NewScraperNotFoundError("LibreDMM", "URL not handled by LibreDMM scraper")
	}

	if !s.enabled {
		return nil, fmt.Errorf("LibreDMM scraper is disabled")
	}

	id, err := s.ExtractIDFromURL(urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to extract ID from URL: %w", err)
	}

	targetURL, ok := normalizeMovieURL(urlStr, s.baseURL)
	if !ok {
		targetURL, err = s.getURLCtx(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to normalize URL: %w", err)
		}
	}

	return s.lookupCtx(ctx, targetURL, id)
}

// ResolveDownloadProxyForHost declares LibreDMM-owned media hosts for downloader proxy routing.
func (s *Scraper) ResolveDownloadProxyForHost(host string) (*config.ProxyConfig, *config.ProxyConfig, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return nil, nil, false
	}
	if host == "libredmm.com" || strings.HasSuffix(host, ".libredmm.com") {
		return s.downloadProxy, s.proxyOverride, true
	}
	return nil, nil, false
}

// ResolveSearchQuery prioritizes this scraper when the input is a LibreDMM URL.
func (s *Scraper) ResolveSearchQuery(input string) (string, bool) {
	normalized, ok := normalizeMovieURL(strings.TrimSpace(input), s.baseURL)
	if !ok {
		return "", false
	}
	return normalized, true
}

// GetURL returns the LibreDMM API URL used to look up the given movie ID.
func (s *Scraper) GetURL(id string) (string, error) {
	return s.getURLCtx(context.Background(), id)
}

func (s *Scraper) getURLCtx(_ context.Context, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("movie ID cannot be empty")
	}

	if normalized, ok := normalizeMovieURL(id, s.baseURL); ok {
		return normalized, nil
	}

	if scraperutil.IsHTTPURL(id) {
		if extracted := extractIDFromURL(id); extracted != "" {
			return buildAPIURL(s.baseURL, extracted), nil
		}
	}

	return buildAPIURL(s.baseURL, id), nil
}

// Search scrapes metadata from the LibreDMM JSON API.
func (s *Scraper) Search(ctx context.Context, id string) (*models.ScraperResult, error) {
	if !s.enabled {
		return nil, fmt.Errorf("LibreDMM scraper is disabled")
	}

	targetURL, err := s.getURLCtx(ctx, id)
	if err != nil {
		return nil, err
	}

	return s.lookupCtx(ctx, targetURL, id)
}

// SearchCandidates implements models.CandidateSearcher. When a code is shared
// by several releases (different makers reusing the same code), every release
// is returned so the user can pick the right one in the manual-search picker.
func (s *Scraper) SearchCandidates(ctx context.Context, query string) ([]*models.SearchCandidate, error) {
	if !s.enabled {
		return nil, fmt.Errorf("LibreDMM scraper is disabled")
	}

	targetURL, err := s.getURLCtx(ctx, query)
	if err != nil {
		return nil, err
	}

	resp, err := s.pollMovieCtx(ctx, targetURL, query)
	if err != nil {
		if se, ok := models.AsScraperError(err); ok && se.Kind == models.ScraperErrorKindNotFound {
			return nil, nil
		}
		return nil, err
	}

	if resp.Movie != nil {
		m := resp.Movie
		code := firstNonEmptyString(m.Code, m.PseudoCode, query)
		return []*models.SearchCandidate{{
			ID:        movieDisplayID(m.BaseCode, code),
			Title:     scraperutil.CleanString(m.Title),
			CoverURL:  firstNonEmptyString(m.Cover.ThumbURL, m.Cover.URL),
			DetailURL: buildPageURL(s.baseURL, code),
			Source:    s.Name(),
		}}, nil
	}

	candidates := make([]*models.SearchCandidate, 0, len(resp.Works))
	for _, w := range resp.Works {
		code := scraperutil.CleanString(w.Code)
		if code == "" {
			continue
		}
		title := scraperutil.CleanString(w.Title)
		if maker := scraperutil.CleanString(w.Maker); maker != "" {
			title = fmt.Sprintf("[%s] %s", maker, title)
		}
		candidates = append(candidates, &models.SearchCandidate{
			ID:        movieDisplayID(w.BaseCode, code),
			Title:     title,
			CoverURL:  firstNonEmptyString(w.ThumbURL, w.CoverURL),
			DetailURL: buildPageURL(s.baseURL, code),
			Source:    s.Name(),
		})
	}
	return candidates, nil
}

// lookupCtx fetches a movie from the API and converts it to a ScraperResult.
// If the code is ambiguous, the first listed release is used.
func (s *Scraper) lookupCtx(ctx context.Context, targetURL, id string) (*models.ScraperResult, error) {
	resp, err := s.pollMovieCtx(ctx, targetURL, id)
	if err != nil {
		return nil, err
	}

	movie := resp.Movie
	if movie == nil && len(resp.Works) > 0 {
		chosen := resp.Works[0]
		logging.Infof("LibreDMM: %s matches %d releases; using %s (%s, %s). Use manual search to pick a different one.",
			id, len(resp.Works), chosen.Code, scraperutil.CleanString(chosen.Maker), chosen.ReleaseDate)

		workResp, err := s.pollMovieCtx(ctx, buildAPIURL(s.baseURL, chosen.Code), id)
		if err != nil {
			return nil, err
		}
		movie = workResp.Movie
	}

	if movie == nil {
		return nil, models.NewScraperNotFoundError("LibreDMM", fmt.Sprintf("movie %s not found on LibreDMM", id))
	}

	result := payloadToResult(movie, s.baseURL, id, s.client.GetClient())
	s.filterPlaceholderScreenshotsCtx(ctx, result)
	return result, nil
}

// pollMovieCtx requests the movie API, retrying while LibreDMM answers
// 202 Accepted (it is fetching a movie it has not seen yet).
func (s *Scraper) pollMovieCtx(ctx context.Context, targetURL, id string) (*apiResponse, error) {
	attempts := s.maxPollAttempts
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		resp, status, retryAfter, err := s.fetchMovieJSONCtx(ctx, targetURL)
		if err != nil {
			return nil, fmt.Errorf("failed to query LibreDMM: %w", err)
		}

		switch {
		case status == http.StatusOK:
			return resp, nil
		case status == http.StatusAccepted:
			if attempt == attempts {
				return nil, models.NewScraperStatusError(
					"LibreDMM",
					status,
					fmt.Sprintf("LibreDMM is still processing %s; try again in a minute", id),
				)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(pollDelay(retryAfter, s.pollInterval)):
			}
		case status == http.StatusNotFound:
			msg := fmt.Sprintf("movie %s not found on LibreDMM", id)
			if detail := resp.problem(); detail != "" {
				msg = fmt.Sprintf("%s (%s)", msg, detail)
			}
			return nil, models.NewScraperNotFoundError("LibreDMM", msg)
		case status == http.StatusBadGateway:
			msg := "LibreDMM is temporarily unavailable (HTTP 502 Bad Gateway; host may be down)"
			if detail := resp.problem(); detail != "" {
				msg += ": " + detail
			}
			return nil, models.NewScraperStatusError("LibreDMM", status, msg)
		default:
			msg := fmt.Sprintf("LibreDMM returned status code %d", status)
			if detail := resp.problem(); detail != "" {
				msg += ": " + detail
			}
			return nil, models.NewScraperStatusError("LibreDMM", status, msg)
		}
	}

	return nil, models.NewScraperNotFoundError("LibreDMM", fmt.Sprintf("movie %s not found on LibreDMM", id))
}

// fetchMovieJSONCtx performs one API request. It returns the decoded body
// (never nil), the HTTP status and the raw Retry-After header.
func (s *Scraper) fetchMovieJSONCtx(ctx context.Context, targetURL string) (*apiResponse, int, string, error) {
	if err := s.rateLimiter.Wait(ctx); err != nil {
		return nil, 0, "", err
	}

	resp, err := s.client.R().SetContext(ctx).Get(targetURL)
	if err != nil {
		return nil, 0, "", err
	}

	status := resp.StatusCode()
	payload := &apiResponse{}
	body := resp.Body()
	if len(body) > 0 {
		// Strip ANSI escape codes and control characters before JSON parsing.
		// These can appear in responses from proxies or terminal wrappers.
		cleanBody := stripANSICodes(string(body))
		if err := json.Unmarshal([]byte(cleanBody), payload); err != nil {
			if status == http.StatusOK {
				return nil, status, "", fmt.Errorf(
					"LibreDMM returned a non-JSON response (content-type %q) from %s; the site's API may have changed: %w",
					resp.Header().Get("Content-Type"), targetURL, err)
			}
			payload = &apiResponse{}
		}
	}

	return payload, status, resp.Header().Get("Retry-After"), nil
}

// pollDelay honours a delta-seconds Retry-After header, capped at
// maxPollInterval, and otherwise falls back to the configured interval.
func pollDelay(retryAfter string, fallback time.Duration) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(retryAfter))
	if err != nil || secs <= 0 {
		return fallback
	}
	d := time.Duration(secs) * time.Second
	if d > maxPollInterval {
		d = maxPollInterval
	}
	return d
}

func payloadToResult(payload *moviePayload, baseURL, fallbackID string, client *http.Client) *models.ScraperResult {
	result := &models.ScraperResult{
		Source:   "libredmm",
		Language: "ja",
	}

	if payload == nil {
		payload = &moviePayload{}
	}

	code := firstNonEmptyString(payload.Code, payload.PseudoCode)

	id := movieDisplayID(payload.BaseCode, code)
	if id == "" {
		id = movieDisplayID("", fallbackID)
	}
	result.ID = id

	pageCode := code
	if pageCode == "" {
		pageCode = id
	}
	if pageCode != "" {
		result.SourceURL = buildPageURL(baseURL, pageCode)
	}

	contentID := preferredContentID(payload.Products)
	if contentID == "" {
		contentID = id
	}
	result.ContentID = contentID

	title := scraperutil.CleanString(payload.Title)
	if title == "" {
		title = id
	}
	result.Title = title
	result.OriginalTitle = title
	result.Description = scraperutil.CleanString(payload.Description)

	if t := parseReleaseDate(payload.ReleaseDate); t != nil {
		result.ReleaseDate = t
	}
	if payload.RuntimeMinutes > 0 {
		result.Runtime = payload.RuntimeMinutes
	}

	result.Director = firstEntityName(payload.Directors)
	result.Maker = firstEntityName(payload.Makers)
	result.Label = firstEntityName(payload.Labels)
	result.Series = firstEntityName(payload.Series)
	result.Genres = dedupeStrings(entityNames(payload.Genres))
	result.Actresses = parseActresses(payload.Actresses, result.SourceURL)

	result.CoverURL = toHTTPS(scraperutil.ResolveURL(result.SourceURL, payload.Cover.URL))
	result.PosterURL = toHTTPS(scraperutil.ResolveURL(result.SourceURL, payload.Cover.ThumbURL))
	if result.CoverURL != "" {
		posterURL, shouldCrop := imageutil.GetOptimalPosterURL(result.CoverURL, client)
		result.ShouldCropPoster = shouldCrop
		if shouldCrop || posterURL == "" {
			result.PosterURL = result.CoverURL
		} else {
			result.PosterURL = posterURL
		}
	}
	if result.PosterURL == "" {
		result.PosterURL = result.CoverURL
	}
	result.ScreenshotURL = dedupeResolvedURLs(payload.SampleImages, result.SourceURL)

	if payload.Review != nil && payload.Review.Average > 0 {
		result.Rating = &models.Rating{
			Score: math.Round(payload.Review.Average*100) / 100,
			Votes: payload.Review.Count,
		}
	}

	return result
}

// movieDisplayID returns the display ID for a movie: the base code when known,
// otherwise the code with any "~anchor" pseudo-code suffix removed.
func movieDisplayID(baseCode, code string) string {
	if id := scraperutil.CleanString(baseCode); id != "" {
		return id
	}
	code = scraperutil.CleanString(code)
	if idx := strings.Index(code, "~"); idx > 0 {
		code = code[:idx]
	}
	return code
}

// preferredContentID returns the DMM content ID of the preferred product,
// falling back to the first product that has one.
func preferredContentID(products []productPayload) string {
	fallback := ""
	for _, p := range products {
		cid := scraperutil.CleanString(p.ContentID)
		if cid == "" {
			continue
		}
		if p.Preferred {
			return cid
		}
		if fallback == "" {
			fallback = cid
		}
	}
	return fallback
}

func parseReleaseDate(raw string) *time.Time {
	raw = scraperutil.CleanString(raw)
	if raw == "" {
		return nil
	}

	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, raw); err == nil {
			return &t
		}
	}
	return nil
}

func parseActresses(entries []actressPayload, base string) []models.ActressInfo {
	seen := make(map[string]bool, len(entries))
	out := make([]models.ActressInfo, 0, len(entries))

	for _, actress := range entries {
		name := scraperutil.CleanString(actress.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		info := models.ActressInfo{
			DMMID:    actress.ID,
			ThumbURL: toHTTPS(scraperutil.ResolveURL(base, actress.ImageURL)),
		}
		if scraperutil.HasJapanese(name) {
			info.JapaneseName = name
		} else {
			parts := strings.Fields(name)
			switch len(parts) {
			case 1:
				info.FirstName = parts[0]
			default:
				info.FirstName = parts[0]
				info.LastName = strings.Join(parts[1:], " ")
			}
		}

		out = append(out, info)
	}

	return out
}

func dedupeResolvedURLs(urls []string, base string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(urls))

	for _, raw := range urls {
		u := normalizeLibredmmScreenshotURL(scraperutil.ResolveURL(base, raw))
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}

	return out
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))

	for _, value := range values {
		v := scraperutil.CleanString(value)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}

	return out
}

func firstNonEmpty(values []string) string {
	for _, v := range values {
		if cleaned := scraperutil.CleanString(v); cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	return firstNonEmpty(values)
}

func entityNames(entities []namedEntity) []string {
	out := make([]string, 0, len(entities))
	for _, e := range entities {
		out = append(out, e.Name)
	}
	return out
}

func firstEntityName(entities []namedEntity) string {
	return firstNonEmpty(entityNames(entities))
}

// normalizeMovieURL converts any recognised LibreDMM URL (movie page, legacy
// .json page, /cid/ page, /search?q= page or API URL) into its API URL.
func normalizeMovieURL(raw, base string) (string, bool) {
	if !scraperutil.IsHTTPURL(raw) {
		return "", false
	}

	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "libredmm.com" && !strings.HasSuffix(host, ".libredmm.com") {
		return "", false
	}

	queryID := scraperutil.CleanString(parsed.Query().Get("q"))
	if queryID != "" && strings.Contains(strings.ToLower(parsed.Path), "/search") {
		return buildAPIURL(base, queryID), true
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	// Strip the API prefix so /api/v1/movies/{id} is handled like /movies/{id}.
	if len(parts) >= 4 && strings.EqualFold(parts[0], "api") && strings.EqualFold(parts[2], "movies") {
		parts = parts[2:]
	}
	if len(parts) >= 2 && (strings.EqualFold(parts[0], "movies") || strings.EqualFold(parts[0], "cid")) {
		id, _ := url.PathUnescape(parts[1])
		id = strings.TrimSuffix(id, ".json")
		id = scraperutil.CleanString(id)
		if id == "" {
			return "", false
		}
		return buildAPIURL(base, id), true
	}

	return "", false
}

// buildAPIURL returns the JSON API URL for a movie code.
func buildAPIURL(baseURL, code string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return baseURL + movieAPIPath + url.PathEscape(strings.TrimSpace(code))
}

// buildPageURL returns the human-facing movie page URL for a movie code.
func buildPageURL(baseURL, code string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return baseURL + "/movies/" + url.PathEscape(strings.TrimSpace(code))
}

func extractIDFromURL(raw string) string {
	if raw == "" {
		return ""
	}
	if matches := cidRegex.FindStringSubmatch(raw); len(matches) > 2 {
		decoded, err := url.QueryUnescape(matches[2])
		if err == nil && strings.TrimSpace(decoded) != "" {
			return strings.TrimSpace(decoded)
		}
		return strings.TrimSpace(matches[2])
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) >= 4 && strings.EqualFold(parts[0], "api") && strings.EqualFold(parts[2], "movies") {
		parts = parts[2:]
	}
	if len(parts) >= 2 {
		if strings.EqualFold(parts[0], "movies") || strings.EqualFold(parts[0], "cid") {
			id, _ := url.PathUnescape(parts[1])
			return strings.TrimSpace(strings.TrimSuffix(id, ".json"))
		}
	}

	return ""
}

func normalizeLibredmmScreenshotURL(raw string) string {
	raw = scraperutil.CleanString(raw)
	if raw == "" {
		return ""
	}

	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	raw = strings.Replace(raw, "awsimgsrc.dmm.co.jp/pics_dig", "pics.dmm.co.jp", 1)

	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}

	u.RawQuery = ""
	u.Fragment = ""

	host := strings.ToLower(u.Hostname())
	if host == "dmm.co.jp" || strings.HasSuffix(host, ".dmm.co.jp") ||
		host == "dmm.com" || strings.HasSuffix(host, ".dmm.com") {
		segments := strings.Split(u.Path, "/")
		for i, seg := range segments {
			if seg == "" {
				continue
			}
			segments[i] = canonicalizeDMMPrefixedContentID(seg)
		}
		u.Path = strings.Join(segments, "/")

		base := path.Base(u.Path)
		lowerBase := strings.ToLower(base)
		if dmmSampleFilenamePattern.MatchString(lowerBase) &&
			strings.Contains(base, "-") &&
			!strings.Contains(lowerBase, "jp-") &&
			!strings.HasSuffix(lowerBase, "pl.jpg") &&
			!strings.HasSuffix(lowerBase, "ps.jpg") {
			base = strings.Replace(base, "-", "jp-", 1)
			u.Path = strings.TrimSuffix(u.Path, path.Base(u.Path)) + base
		}
	}

	return u.String()
}

func canonicalizeDMMPrefixedContentID(seg string) string {
	ext := ""
	if idx := strings.LastIndex(seg, "."); idx > 0 {
		ext = seg[idx:]
		seg = seg[:idx]
	}

	suffix := ""
	lower := strings.ToLower(seg)
	for _, marker := range []string{"jp-", "pl", "ps"} {
		if marker == "jp-" {
			if idx := strings.Index(lower, marker); idx > 0 {
				suffix = seg[idx:]
				seg = seg[:idx]
				lower = strings.ToLower(seg)
				break
			}
			continue
		}
		if strings.HasSuffix(lower, marker) && len(seg) > len(marker) {
			suffix = seg[len(seg)-len(marker):]
			seg = seg[:len(seg)-len(marker)]
			lower = strings.ToLower(seg)
			break
		}
	}

	if matches := dmmPrefixedCIDRegex.FindStringSubmatch(lower); len(matches) == 3 {
		tail := matches[2]
		digitPrefixLen := 0
		for digitPrefixLen < len(tail) && tail[digitPrefixLen] >= '0' && tail[digitPrefixLen] <= '9' {
			digitPrefixLen++
		}
		if digitPrefixLen > 0 && digitPrefixLen < 3 {
			tail = strings.Repeat("0", 3-digitPrefixLen) + tail
		}
		seg = matches[1] + tail
	}

	return seg + suffix + ext
}

func toHTTPS(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "http://") {
		return "https://" + strings.TrimPrefix(raw, "http://")
	}
	return raw
}

// stripANSICodes removes ANSI escape sequences and control characters from a string.
// These can appear in responses from proxies or terminal wrappers.
// Handles ANSI sequences, bare ESC characters, and other control characters.
// Also attempts to extract valid JSON from garbage data.
func stripANSICodes(s string) string {
	// First strip standard ANSI sequences
	s = ansiEscapeRegex.ReplaceAllString(s, "")
	// Then strip any remaining bare ESC characters
	s = escCharRegex.ReplaceAllString(s, "")
	// Finally strip other control characters (except tab, newline, CR)
	s = controlCharRegex.ReplaceAllString(s, "")

	// If the string doesn't start with valid JSON (object, array or null),
	// try to find the start of the JSON content
	s = strings.TrimSpace(s)
	if len(s) > 0 && s[0] != '{' && s[0] != '[' && !strings.HasPrefix(s, "null") {
		// Find the first '{' or '[' which should be the start of JSON
		for i, c := range s {
			if c == '{' || c == '[' {
				s = s[i:]
				break
			}
		}
	}

	return s
}

func (s *Scraper) filterPlaceholderScreenshotsCtx(ctx context.Context, result *models.ScraperResult) {
	if len(result.ScreenshotURL) == 0 {
		return
	}

	cfg := placeholder.ConfigFromSettings(&s.settings, placeholder.DefaultDMMPlaceholderHashes)
	if !cfg.Enabled {
		return
	}

	filtered, count, err := placeholder.FilterURLs(ctx, s.client, result.ScreenshotURL, cfg)
	if err != nil {
		logging.Warnf("libredmm: placeholder filter error: %v", err)
		return
	}
	if count > 0 {
		logging.Debugf("libredmm: Filtered %d placeholder screenshots", count)
		result.ScreenshotURL = filtered
	}
}
