// Additional test cases for libredmm scraper coverage

package libredmm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/javinizer/javinizer-go/internal/config"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/javinizer/javinizer-go/internal/scraperutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseReleaseDate tests release date parsing
func TestParseReleaseDate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Time
		hasWant bool
	}{
		{
			name:    "RFC3339 format",
			input:   "2024-01-15T00:00:00Z",
			want:    time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			hasWant: true,
		},
		{
			name:    "RFC3339 with fractional seconds",
			input:   "2024-01-15T12:30:45.123456Z",
			want:    time.Date(2024, 1, 15, 12, 30, 45, 123456000, time.UTC),
			hasWant: true,
		},
		{
			name:    "Simple date format",
			input:   "2024-01-15",
			want:    time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			hasWant: true,
		},
		{
			name:    "DateTime format",
			input:   "2024-01-15 12:30:45",
			want:    time.Date(2024, 1, 15, 12, 30, 45, 0, time.UTC),
			hasWant: true,
		},
		{
			name:    "empty string",
			input:   "",
			hasWant: false,
		},
		{
			name:    "invalid format",
			input:   "invalid",
			hasWant: false,
		},
		{
			name:    "Japanese format",
			input:   "2024/01/15",
			hasWant: false,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			hasWant: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseReleaseDate(tt.input)

			if tt.hasWant {
				require.NotNil(t, result)
				assert.Equal(t, tt.want.Unix(), result.Unix())
			} else {
				assert.Nil(t, result)
			}
		})
	}
}

// TestParseActresses tests actress parsing
func TestParseActresses(t *testing.T) {
	tests := []struct {
		name    string
		entries []actressPayload
		base    string
		wantLen int
	}{
		{
			name: "single Western actress",
			entries: []actressPayload{
				{Name: "Alice Johnson", ImageURL: "https://example.com/alice.jpg"},
			},
			base:    "https://www.libredmm.com",
			wantLen: 1,
		},
		{
			name: "single Japanese actress",
			entries: []actressPayload{
				{Name: "山田花子", ImageURL: "https://example.com/hanako.jpg"},
			},
			base:    "https://www.libredmm.com",
			wantLen: 1,
		},
		{
			name: "multiple actresses",
			entries: []actressPayload{
				{Name: "Alice", ImageURL: "https://example.com/alice.jpg"},
				{Name: "Bob", ImageURL: "https://example.com/bob.jpg"},
			},
			base:    "https://www.libredmm.com",
			wantLen: 2,
		},
		{
			name: "duplicate actresses deduplicated",
			entries: []actressPayload{
				{Name: "Alice", ImageURL: "https://example.com/alice.jpg"},
				{Name: "Alice", ImageURL: "https://example.com/alice2.jpg"},
			},
			base:    "https://www.libredmm.com",
			wantLen: 1,
		},
		{
			name:    "empty entries",
			entries: []actressPayload{},
			base:    "https://www.libredmm.com",
			wantLen: 0,
		},
		{
			name: "empty name skipped",
			entries: []actressPayload{
				{Name: "", ImageURL: "https://example.com/empty.jpg"},
				{Name: "Valid", ImageURL: "https://example.com/valid.jpg"},
			},
			base:    "https://www.libredmm.com",
			wantLen: 1,
		},
		{
			name: "empty image URL",
			entries: []actressPayload{
				{Name: "Alice", ImageURL: ""},
			},
			base:    "https://www.libredmm.com",
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseActresses(tt.entries, tt.base)
			assert.Len(t, result, tt.wantLen)
		})
	}
}

// TestNormalizeMovieURL tests URL normalization to the JSON API endpoint
func TestNormalizeMovieURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		base  string
		want  string
		ok    bool
	}{
		{
			name:  "legacy movie URL with JSON suffix",
			input: "https://www.libredmm.com/movies/IPX-535.json",
			base:  "https://www.libredmm.com",
			want:  "https://www.libredmm.com/api/v1/movies/IPX-535",
			ok:    true,
		},
		{
			name:  "movie page URL",
			input: "https://www.libredmm.com/movies/IPX-535",
			base:  "https://www.libredmm.com",
			want:  "https://www.libredmm.com/api/v1/movies/IPX-535",
			ok:    true,
		},
		{
			name:  "movie page URL with pseudo code",
			input: "https://www.libredmm.com/movies/SW-001~45262",
			base:  "https://www.libredmm.com",
			want:  "https://www.libredmm.com/api/v1/movies/SW-001~45262",
			ok:    true,
		},
		{
			name:  "API URL is idempotent",
			input: "https://www.libredmm.com/api/v1/movies/IPX-535",
			base:  "https://www.libredmm.com",
			want:  "https://www.libredmm.com/api/v1/movies/IPX-535",
			ok:    true,
		},
		{
			name:  "cid URL",
			input: "https://www.libredmm.com/cid/ipx00535",
			base:  "https://www.libredmm.com",
			want:  "https://www.libredmm.com/api/v1/movies/ipx00535",
			ok:    true,
		},
		{
			name:  "search URL with query",
			input: "https://www.libredmm.com/search?q=IPX535",
			base:  "https://www.libredmm.com",
			want:  "https://www.libredmm.com/api/v1/movies/IPX535",
			ok:    true,
		},
		{
			name:  "search URL with CID parameter",
			input: "https://www.libredmm.com/search?cid=IPX-535",
			base:  "https://www.libredmm.com",
			want:  "", // normalizeMovieURL only handles 'q' parameter, not 'cid'
			ok:    false,
		},
		{
			name:  "non-HTTP URL",
			input: "IPX535",
			base:  "https://www.libredmm.com",
			want:  "",
			ok:    false,
		},
		{
			name:  "non-libredmm URL",
			input: "https://www.mgstage.com/product/MIDE-123",
			base:  "https://www.libredmm.com",
			want:  "",
			ok:    false,
		},
		{
			name:  "invalid URL",
			input: "not-a-url",
			base:  "https://www.libredmm.com",
			want:  "",
			ok:    false,
		},
		{
			name:  "URL with special characters in path",
			input: "https://www.libredmm.com/movies/IPX%20535",
			base:  "https://www.libredmm.com",
			want:  "https://www.libredmm.com/api/v1/movies/IPX%20535",
			ok:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, ok := normalizeMovieURL(tt.input, tt.base)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestBuildAPIURL tests API and page URL building
func TestBuildAPIURL(t *testing.T) {
	assert.Equal(t, "https://www.libredmm.com/api/v1/movies/IPX-535", buildAPIURL("https://www.libredmm.com", "IPX-535"))
	assert.Equal(t, "https://www.libredmm.com/api/v1/movies/IPX-535", buildAPIURL("https://www.libredmm.com/", " IPX-535 "))
	assert.Equal(t, "https://www.libredmm.com/api/v1/movies/IPX%20535", buildAPIURL("https://www.libredmm.com", "IPX 535"))
	assert.Equal(t, "https://www.libredmm.com/api/v1/movies/a%2Fb", buildAPIURL("https://www.libredmm.com", "a/b"))
	assert.Equal(t, "https://www.libredmm.com/movies/SW-001~45262", buildPageURL("https://www.libredmm.com/", "SW-001~45262"))
}

// TestPollDelay tests Retry-After handling
func TestPollDelay(t *testing.T) {
	fallback := 1500 * time.Millisecond
	assert.Equal(t, fallback, pollDelay("", fallback))
	assert.Equal(t, fallback, pollDelay("garbage", fallback))
	assert.Equal(t, fallback, pollDelay("0", fallback))
	assert.Equal(t, 3*time.Second, pollDelay("3", fallback))
	assert.Equal(t, maxPollInterval, pollDelay("30", fallback))
}

// TestMovieDisplayID tests display ID derivation
func TestMovieDisplayID(t *testing.T) {
	assert.Equal(t, "SW-001", movieDisplayID("SW-001", "SW-001~45262"))
	assert.Equal(t, "SW-001", movieDisplayID("", "SW-001~45262"))
	assert.Equal(t, "IPX-123", movieDisplayID("", "IPX-123"))
	assert.Equal(t, "", movieDisplayID("", ""))
}

// TestExtractIDFromURL tests ID extraction from URLs
func TestExtractIDFromURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "movie URL singular (not supported)",
			url:  "https://www.libredmm.com/movie/ipx-535",
			want: "",
		},
		{
			name: "movie URL with JSON suffix singular (not supported)",
			url:  "https://www.libredmm.com/movie/ipx-535.json",
			want: "",
		},
		{
			name: "movies URL plural",
			url:  "https://www.libredmm.com/movies/IPX-535",
			want: "IPX-535",
		},
		{
			name: "search URL with q parameter (not supported, use cid)",
			url:  "https://www.libredmm.com/search?q=ipx-535",
			want: "",
		},
		{
			name: "search URL with cid parameter",
			url:  "https://www.libredmm.com/search?cid=ipx-535",
			want: "ipx-535",
		},
		{
			name: "URL with CID in path",
			url:  "https://www.libredmm.com/movie/?cid=ipx-535",
			want: "ipx-535",
		},
		{
			name: "empty URL",
			url:  "",
			want: "",
		},
		{
			name: "invalid URL",
			url:  "not-a-url",
			want: "",
		},
		{
			name: "URL with special characters",
			url:  "https://www.libredmm.com/movies/IPX%20535",
			want: "IPX 535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractIDFromURL(tt.url)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestResolveURL tests URL resolution
func TestResolveURL(t *testing.T) {
	tests := []struct {
		name string
		base string
		raw  string
		want string
	}{
		{
			name: "absolute URL unchanged",
			base: "https://example.com/foo/bar",
			raw:  "https://other.com/x",
			want: "https://other.com/x",
		},
		{
			name: "protocol-relative URL",
			base: "https://example.com/foo/bar",
			raw:  "//cdn.example.com/image.jpg",
			want: "https://cdn.example.com/image.jpg",
		},
		{
			name: "root-relative URL",
			base: "https://example.com/foo/bar",
			raw:  "/baz",
			want: "https://example.com/baz",
		},
		{
			name: "relative path",
			base: "https://example.com/foo/bar",
			raw:  "qux",
			want: "https://example.com/foo/qux",
		},
		{
			name: "parent directory",
			base: "https://example.com/foo/bar",
			raw:  "../baz",
			want: "https://example.com/baz",
		},
		{
			name: "empty raw URL",
			base: "https://example.com/foo/bar",
			raw:  "",
			want: "",
		},
		{
			name: "whitespace only",
			base: "https://example.com/foo/bar",
			raw:  "   ",
			want: "",
		},
		{
			name: "URL with query params",
			base: "https://example.com/foo/bar",
			raw:  "image.jpg?foo=bar",
			// Shared ResolveURL correctly resolves relative URLs with query params
			want: "https://example.com/foo/image.jpg?foo=bar",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scraperutil.ResolveURL(tt.base, tt.raw)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestDedupeStrings tests string deduplication
func TestDedupeStrings(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "simple deduplication",
			input: []string{"a", "b", "a", "c"},
			want:  []string{"a", "b", "c"},
		},
		{
			name:  "all duplicates",
			input: []string{"a", "a", "a"},
			want:  []string{"a"},
		},
		{
			name:  "empty input",
			input: []string{},
			want:  []string{},
		},
		{
			name:  "empty strings filtered out",
			input: []string{"", "a", "", "b", ""},
			want:  []string{"a", "b"},
		},
		{
			name:  "whitespace strings filtered out",
			input: []string{"  ", "a", "   ", "b"},
			want:  []string{"a", "b"},
		},
		{
			name:  "case sensitive",
			input: []string{"A", "a", "B"},
			want:  []string{"A", "a", "B"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dedupeStrings(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestFirstNonEmpty tests first non-empty string
func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{
			name:  "first non-empty",
			input: []string{"", "", "hello", ""},
			want:  "hello",
		},
		{
			name:  "all empty",
			input: []string{"", "", ""},
			want:  "",
		},
		{
			name:  "empty input",
			input: []string{},
			want:  "",
		},
		{
			name:  "first is non-empty",
			input: []string{"first", "second", "third"},
			want:  "first",
		},
		{
			name:  "whitespace filtered",
			input: []string{"", "  ", "valid"},
			want:  "valid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := firstNonEmpty(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestCleanString tests string cleaning
func TestCleanString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "whitespace trimmed",
			input: "  hello world  ",
			want:  "hello world",
		},
		{
			name:  "non-breaking spaces replaced",
			input: "hello\u00a0world",
			want:  "hello world",
		},
		{
			name:  "multiple spaces collapsed",
			input: "hello    world",
			want:  "hello world",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only whitespace",
			input: "   \t  ",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scraperutil.CleanString(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestHasJapanese tests Japanese character detection
func TestHasJapanese(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "kanji only",
			input: "山田",
			want:  true,
		},
		{
			name:  "hiragana only",
			input: "やまだ",
			want:  true,
		},
		{
			name:  "katakana only",
			input: "ヤマダ",
			want:  true,
		},
		{
			name:  "mixed Japanese and English",
			input: "山田 Alice",
			want:  true,
		},
		{
			name:  "English only",
			input: "Alice Johnson",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "numbers and symbols",
			input: "123-456",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scraperutil.HasJapanese(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestToHTTPS tests HTTP to HTTPS conversion
func TestToHTTPS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "http converted to https",
			input: "http://example.com/image.jpg",
			want:  "https://example.com/image.jpg",
		},
		{
			name:  "https unchanged",
			input: "https://example.com/image.jpg",
			want:  "https://example.com/image.jpg",
		},
		{
			name:  "already https with path",
			input: "https://cdn.example.com/foo/bar.jpg",
			want:  "https://cdn.example.com/foo/bar.jpg",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace trimmed and converted",
			input: "  http://example.com  ",
			want:  "https://example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toHTTPS(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestIsHTTPURL tests HTTP URL detection
func TestIsHTTPURL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "http URL",
			input: "http://example.com",
			want:  true,
		},
		{
			name:  "https URL",
			input: "https://example.com",
			want:  true,
		},
		{
			name:  "ftp URL",
			input: "ftp://example.com",
			want:  false,
		},
		{
			name:  "no scheme",
			input: "example.com",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "invalid URL",
			input: "not-a-url",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scraperutil.IsHTTPURL(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestWaitForRateLimit tests rate limiting
// TestPayloadToResult tests result conversion from the API movie payload
func TestPayloadToResult(t *testing.T) {
	payload := &moviePayload{
		Code:           "IPX-123",
		BaseCode:       "IPX-123",
		PseudoCode:     "IPX-123~1219",
		Title:          " Test Movie ",
		ReleaseDate:    "2018-03-31",
		RuntimeMinutes: 119,
		Description:    "Test description",
		Cover: coverPayload{
			URL:      "https://pics.dmm.co.jp/digital/video/ipx00123/ipx00123pl.jpg",
			ThumbURL: "https://pics.dmm.co.jp/digital/video/ipx00123/ipx00123ps.jpg",
		},
		SampleImages: []string{
			"https://pics.dmm.co.jp/digital/video/ipx00123/ipx00123jp-1.jpg",
			"https://pics.dmm.co.jp/digital/video/ipx00123/ipx00123jp-1.jpg",
		},
		Review:    &reviewPayload{Average: 3.2899999618530273, Count: 7},
		Actresses: []actressPayload{{ID: 1001592, Name: "天海つばさ", Ruby: "あまみつばさ", ImageURL: "http://pics.dmm.co.jp/mono/actjpgs/amami_tubasa.jpg"}},
		Makers:    []namedEntity{{ID: 1219, Name: "アイデアポケット"}},
		Labels:    []namedEntity{{ID: 1561, Name: "ティッシュ"}},
		Series:    []namedEntity{},
		Directors: []namedEntity{{ID: 290, Name: "ザック荒井"}},
		Genres:    []namedEntity{{ID: 5005, Name: "アナル"}, {ID: 1, Name: "アナル"}, {ID: 2, Name: "単体作品"}},
		Products: []productPayload{
			{ContentID: "ipx00123dl", Preferred: false},
			{ContentID: "ipx00123", Preferred: true},
		},
	}

	client := &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network disabled")
		}),
	}

	result := payloadToResult(payload, "https://www.libredmm.com", "ipx123", client)
	require.NotNil(t, result)
	assert.Equal(t, "libredmm", result.Source)
	assert.Equal(t, "ja", result.Language)
	assert.Equal(t, "IPX-123", result.ID)
	assert.Equal(t, "ipx00123", result.ContentID)
	assert.Equal(t, "https://www.libredmm.com/movies/IPX-123", result.SourceURL)
	assert.Equal(t, "Test Movie", result.Title)
	assert.Equal(t, "Test Movie", result.OriginalTitle)
	assert.Equal(t, "Test description", result.Description)
	require.NotNil(t, result.ReleaseDate)
	assert.Equal(t, "2018-03-31", result.ReleaseDate.Format("2006-01-02"))
	assert.Equal(t, 119, result.Runtime)
	assert.Equal(t, "ザック荒井", result.Director)
	assert.Equal(t, "アイデアポケット", result.Maker)
	assert.Equal(t, "ティッシュ", result.Label)
	assert.Equal(t, "", result.Series)
	assert.Equal(t, []string{"アナル", "単体作品"}, result.Genres)
	require.Len(t, result.Actresses, 1)
	assert.Equal(t, "天海つばさ", result.Actresses[0].JapaneseName)
	assert.Equal(t, 1001592, result.Actresses[0].DMMID)
	assert.Equal(t, "https://pics.dmm.co.jp/mono/actjpgs/amami_tubasa.jpg", result.Actresses[0].ThumbURL)
	assert.Equal(t, payload.Cover.URL, result.CoverURL)
	assert.Equal(t, []string{"https://pics.dmm.co.jp/digital/video/ipx00123/ipx00123jp-1.jpg"}, result.ScreenshotURL)
	require.NotNil(t, result.Rating)
	assert.Equal(t, 3.29, result.Rating.Score)
	assert.Equal(t, 7, result.Rating.Votes)
}

// TestPayloadToResult_Fallbacks tests nil/empty payloads and pseudo codes
func TestPayloadToResult_Fallbacks(t *testing.T) {
	t.Run("nil payload uses fallback ID", func(t *testing.T) {
		result := payloadToResult(nil, "https://www.libredmm.com", "IPX535", nil)
		require.NotNil(t, result)
		assert.Equal(t, "IPX535", result.ID)
		assert.Equal(t, "IPX535", result.ContentID)
		assert.Equal(t, "IPX535", result.Title)
		assert.Nil(t, result.Rating)
		assert.Equal(t, 0, result.Runtime)
	})

	t.Run("pseudo code keeps exact source URL but display ID", func(t *testing.T) {
		result := payloadToResult(&moviePayload{Code: "SW-001~45262", Title: "x"}, "https://www.libredmm.com", "SW-001", nil)
		assert.Equal(t, "SW-001", result.ID)
		assert.Equal(t, "https://www.libredmm.com/movies/SW-001~45262", result.SourceURL)
	})

	t.Run("zero review not set", func(t *testing.T) {
		result := payloadToResult(&moviePayload{Code: "A-1", Review: &reviewPayload{Average: 0, Count: 0}}, "https://www.libredmm.com", "A-1", nil)
		assert.Nil(t, result.Rating)
	})

	t.Run("content ID falls back to first product", func(t *testing.T) {
		result := payloadToResult(&moviePayload{Code: "A-1", Products: []productPayload{{ContentID: ""}, {ContentID: "a00001"}}}, "https://www.libredmm.com", "A-1", nil)
		assert.Equal(t, "a00001", result.ContentID)
	})
}

// TestResolveSearchQuery tests search query resolution
func TestResolveSearchQuery(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		ok    bool
	}{
		{
			name:  "libredmm movie URL",
			input: "https://www.libredmm.com/movies/IPX-535",
			want:  "https://www.libredmm.com/api/v1/movies/IPX-535",
			ok:    true,
		},
		{
			name:  "libredmm search URL",
			input: "https://www.libredmm.com/search?q=IPX535",
			want:  "https://www.libredmm.com/api/v1/movies/IPX535",
			ok:    true,
		},
		{
			name:  "non-libredmm URL",
			input: "https://www.mgstage.com/product/MIDE-123",
			want:  "",
			ok:    false,
		},
		{
			name:  "plain ID",
			input: "IPX535",
			want:  "",
			ok:    false,
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
			ok:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := testSettings("https://www.libredmm.com")
			scraper := New(settings, nil, config.FlareSolverrConfig{})

			result, ok := scraper.ResolveSearchQuery(tt.input)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestGetURLErrorPaths tests GetURL error scenarios
func TestGetURLErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "empty ID",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: true,
		},
		{
			name:    "plain ID returns API URL",
			input:   "IPX535",
			wantErr: false,
		},
		{
			name:    "movie ID with hyphen",
			input:   "IPX-535",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := testSettings("https://www.libredmm.com")
			scraper := New(settings, nil, config.FlareSolverrConfig{})

			url, err := scraper.GetURL(tt.input)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Empty(t, url)
			} else {
				assert.NoError(t, err)
				assert.Contains(t, url, "libredmm.com")
			}
		})
	}
}

// newTestScraper builds a scraper pointed at a test server with fast polling.
func newTestScraper(t *testing.T, handler http.HandlerFunc) *Scraper {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	settings := testSettings(server.URL)
	scraper := New(settings, nil, config.FlareSolverrConfig{})
	scraper.maxPollAttempts = 3
	scraper.pollInterval = 1 * time.Millisecond
	return scraper
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = fmt.Fprint(w, body)
}

const testMovieJSON = `{"movie":{"code":"IPX-123","base_code":"IPX-123","pseudo_code":"IPX-123~1219","title":"Title","release_date":"2018-03-31","runtime_minutes":119,"cover":{"url":"","thumb_url":""},"sample_images":[],"actresses":[{"id":1,"name":"天海つばさ"}],"makers":[{"id":1219,"name":"Maker"}],"labels":[],"series":[{"id":9,"name":"Series"}],"directors":[],"genres":[{"id":1,"name":"Genre"}],"products":[{"content_id":"ipx00123","preferred":true}]}}`

// TestSearch_Found tests a straightforward 200 response
func TestSearch_Found(t *testing.T) {
	scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/movies/IPX-123", r.URL.Path)
		writeJSON(w, http.StatusOK, testMovieJSON)
	})

	result, err := scraper.Search(context.Background(), "IPX-123")
	require.NoError(t, err)
	assert.Equal(t, "IPX-123", result.ID)
	assert.Equal(t, "ipx00123", result.ContentID)
	assert.Equal(t, "Title", result.Title)
	assert.Equal(t, "Maker", result.Maker)
	assert.Equal(t, "Series", result.Series)
	assert.Equal(t, 119, result.Runtime)
	assert.Contains(t, result.SourceURL, "/movies/IPX-123")
}

// TestSearch_PendingThenFound tests polling through 202 Accepted
func TestSearch_PendingThenFound(t *testing.T) {
	calls := 0
	scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			writeJSON(w, http.StatusAccepted, "null")
			return
		}
		writeJSON(w, http.StatusOK, testMovieJSON)
	})

	result, err := scraper.Search(context.Background(), "IPX-123")
	require.NoError(t, err)
	assert.Equal(t, "IPX-123", result.ID)
	assert.Equal(t, 2, calls)
}

// TestSearchProcessingTimeout tests giving up after repeated 202 responses
func TestSearchProcessingTimeout(t *testing.T) {
	calls := 0
	scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeJSON(w, http.StatusAccepted, "null")
	})
	scraper.maxPollAttempts = 2

	result, err := scraper.Search(context.Background(), "ZZZ-99999")
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "still processing")
	assert.Equal(t, 2, calls)
}

// TestSearch_NotFound tests problem+json 404 responses
func TestSearch_NotFound(t *testing.T) {
	scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"title":"Not Found","status":404,"detail":"movie not found on Fanza; it will be looked up again later"}`)
	})

	result, err := scraper.Search(context.Background(), "ZZZ-99999")
	require.Error(t, err)
	assert.Nil(t, result)
	se, ok := models.AsScraperError(err)
	require.True(t, ok)
	assert.Equal(t, models.ScraperErrorKindNotFound, se.Kind)
	assert.Contains(t, err.Error(), "looked up again later")
}

// TestSearch_ServerError tests non-404 error statuses
func TestSearch_ServerError(t *testing.T) {
	scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, `{"title":"Bad Gateway","status":502,"detail":"upstream down"}`)
	})

	_, err := scraper.Search(context.Background(), "IPX-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
	assert.Contains(t, err.Error(), "upstream down")
}

// TestSearch_HTMLResponse tests a clear error when the site returns HTML
// (what happened when the old /search?format=json endpoint stopped working).
func TestSearch_HTMLResponse(t *testing.T) {
	scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, `<!doctype html><html><head><link href="/_app/x.css"></head></html>`)
	})

	_, err := scraper.Search(context.Background(), "IPX-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-JSON response")
	assert.Contains(t, err.Error(), "text/html")
}

const testWorksJSON = `{"code":"SW-001","works":[` +
	`{"code":"SW-001~45914","base_code":"SW-001","maker":"SWITCH","title":"First","release_date":"2011-01-08","thumb_url":"https://pics.dmm.co.jp/a/ps.jpg","cover_url":"https://pics.dmm.co.jp/a/pl.jpg"},` +
	`{"code":"SW-001~45262","base_code":"SW-001","maker":"プラム","title":"Second","release_date":"2009-11-30","thumb_url":"","cover_url":"https://pics.dmm.co.jp/b/pl.jpg"}]}`

// TestSearch_AmbiguousCodeUsesFirstWork tests the multi-release response
func TestSearch_AmbiguousCodeUsesFirstWork(t *testing.T) {
	var paths []string
	scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/movies/SW-001":
			writeJSON(w, http.StatusOK, testWorksJSON)
		case "/api/v1/movies/SW-001~45914":
			writeJSON(w, http.StatusOK, `{"movie":{"code":"SW-001~45914","base_code":"SW-001","pseudo_code":"SW-001~45914","title":"First"}}`)
		default:
			http.NotFound(w, r)
		}
	})

	result, err := scraper.Search(context.Background(), "SW-001")
	require.NoError(t, err)
	assert.Equal(t, "SW-001", result.ID)
	assert.Equal(t, "First", result.Title)
	assert.Contains(t, result.SourceURL, "/movies/SW-001~45914")
	assert.Equal(t, []string{"/api/v1/movies/SW-001", "/api/v1/movies/SW-001~45914"}, paths)
}

// TestScrapeURL_PseudoCodePage tests scraping a picked candidate's page URL
func TestScrapeURL_PseudoCodePage(t *testing.T) {
	var gotPath string
	scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeJSON(w, http.StatusOK, `{"movie":{"code":"SW-001~45262","base_code":"SW-001","title":"Second"}}`)
	})

	result, err := scraper.ScrapeURL(context.Background(), "https://www.libredmm.com/movies/SW-001~45262")
	require.NoError(t, err)
	assert.Equal(t, "/api/v1/movies/SW-001~45262", gotPath)
	assert.Equal(t, "SW-001", result.ID)
	assert.Equal(t, "Second", result.Title)
}

// TestSearchCandidates tests the manual-search candidate picker integration
func TestSearchCandidates(t *testing.T) {
	t.Run("ambiguous code lists every release", func(t *testing.T) {
		scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, testWorksJSON)
		})

		candidates, err := scraper.SearchCandidates(context.Background(), "SW-001")
		require.NoError(t, err)
		require.Len(t, candidates, 2)
		assert.Equal(t, "SW-001", candidates[0].ID)
		assert.Equal(t, "[SWITCH] First", candidates[0].Title)
		assert.Equal(t, "https://pics.dmm.co.jp/a/ps.jpg", candidates[0].CoverURL)
		assert.Equal(t, scraper.baseURL+"/movies/SW-001~45914", candidates[0].DetailURL)
		assert.Equal(t, "libredmm", candidates[0].Source)
		assert.Equal(t, "https://pics.dmm.co.jp/b/pl.jpg", candidates[1].CoverURL)
	})

	t.Run("single movie returns one candidate", func(t *testing.T) {
		scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, testMovieJSON)
		})

		candidates, err := scraper.SearchCandidates(context.Background(), "IPX-123")
		require.NoError(t, err)
		require.Len(t, candidates, 1)
		assert.Equal(t, "IPX-123", candidates[0].ID)
		assert.Equal(t, scraper.baseURL+"/movies/IPX-123", candidates[0].DetailURL)
	})

	t.Run("not found returns no candidates and no error", func(t *testing.T) {
		scraper := newTestScraper(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusNotFound, `{"title":"Not Found","status":404,"detail":"movie not found"}`)
		})

		candidates, err := scraper.SearchCandidates(context.Background(), "NOPE-1")
		require.NoError(t, err)
		assert.Empty(t, candidates)
	})
}

// TestPayloadToResult_CoverURLFallback tests cover URL fallback when poster probe fails
func TestPayloadToResult_CoverURLFallback(t *testing.T) {
	coverURL := "https://pics.dmm.co.jp/digital/video/118abp00880/118abp00880pl.jpg"
	payload := &moviePayload{
		Code:     "ABP-880",
		BaseCode: "ABP-880",
		Title:    "Movie Title",
		Cover: coverPayload{
			URL:      coverURL,
			ThumbURL: "https://pics.dmm.co.jp/digital/video/118abp00880/118abp00880ps.jpg",
		},
		Products: []productPayload{{ContentID: "118abp880", Preferred: true}},
	}

	// Force poster probe to fail immediately
	client := &http.Client{
		Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("network disabled")
		}),
	}

	result := payloadToResult(payload, "https://www.libredmm.com", "ABP-880", client)
	require.NotNil(t, result)
	assert.Equal(t, coverURL, result.CoverURL)
	assert.Equal(t, coverURL, result.PosterURL)
}

// TestNormalizeLibredmmScreenshotURL_DMMHosts tests DMM screenshot normalization
func TestNormalizeLibredmmScreenshotURL_DMMHosts(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "DMM sample image gets jp marker",
			input: "https://pics.dmm.co.jp/digital/video/ipx00535/ipx00535-1.jpg?foo=bar",
			want:  "https://pics.dmm.co.jp/digital/video/ipx00535/ipx00535jp-1.jpg",
		},
		{
			name:  "prefixed content ID normalized",
			input: "https://pics.dmm.co.jp/digital/video/118abp00880/118abp00880jp-2.jpg",
			want:  "https://pics.dmm.co.jp/digital/video/118abp880/118abp880jp-2.jpg",
		},
		{
			name:  "atwsimgsrc.dmm.co.jp domain redirected",
			input: "https://awsimgsrc.dmm.co.jp/pics_dig/video/ipx-123/image.jpg",
			want:  "https://pics.dmm.co.jp/video/ipx-123/image.jpg",
		},
		{
			name:  "non-DMM URL unchanged",
			input: "https://example.com/image.jpg",
			want:  "https://example.com/image.jpg",
		},
		{
			name:  "query params removed",
			input: "https://pics.dmm.co.jp/digital/video/test/image.jpg?param=value",
			want:  "https://pics.dmm.co.jp/digital/video/test/image.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeLibredmmScreenshotURL(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestCanonicalizeDMMPrefixedContentID tests content ID canonicalization
func TestCanonicalizeDMMPrefixedContentID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "prefix with leading zeros removed - 3+ digit prefix preserved",
			input: "118abp00880jp-1.jpg",
			// Regex ^(\d{3,}[a-z]+)0+(\d+.*)$ matches 118abp (prefix), 00 (zeros), 880jp-1 (tail)
			// Since tail starts with 3 digits, no padding needed
			want: "118abp880jp-1.jpg",
		},
		{
			name:  "jp marker preserved",
			input: "ipx-535jp.jpg",
			want:  "ipx-535jp.jpg",
		},
		{
			name:  "pl suffix preserved",
			input: "abp-123pl.jpg",
			want:  "abp-123pl.jpg",
		},
		{
			name:  "ps suffix preserved",
			input: "abw-456ps.jpg",
			want:  "abw-456ps.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := canonicalizeDMMPrefixedContentID(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// TestHasJapanese_FullNames tests full Japanese name detection
func TestHasJapanese_FullNames(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "full name with kanji",
			input: "山田花子",
			want:  true,
		},
		{
			name:  "full name with hiragana",
			input: "やまだはなこ",
			want:  true,
		},
		{
			name:  "full name with katakana",
			input: "ヤマダハナコ",
			want:  true,
		},
		{
			name:  "full name with mixed scripts",
			input: "山田 ハナコ",
			want:  true,
		},
		{
			name:  "English name",
			input: "Alice Johnson",
			want:  false,
		},
		{
			name:  "mixed English and Japanese",
			input: "Alice 山田",
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := scraperutil.HasJapanese(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

// ========== Helper types and functions for testing ==========

// testSettings creates test scraper settings for LibreDMM
func testSettings(baseURL string) config.ScraperSettings {
	return config.ScraperSettings{
		Enabled:   true,
		RateLimit: 0,
		BaseURL:   baseURL,
	}
}

// roundTripperFunc is a test helper for HTTP round tripper
type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
