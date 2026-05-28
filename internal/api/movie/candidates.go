package movie

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/javinizer/javinizer-go/internal/logging"
	"github.com/javinizer/javinizer-go/internal/models"
)

type SearchCandidatesRequest struct {
	Query    string   `json:"query"`
	Scrapers []string `json:"scrapers"`
}

type SearchCandidatesResponse struct {
	Candidates []*models.SearchCandidate `json:"candidates"`
}

// searchCandidates returns a lightweight list of search candidates from scrapers
// that implement models.CandidateSearcher, without fetching each detail page.
func searchCandidates(deps *ServerDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req SearchCandidatesRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}

		req.Query = strings.TrimSpace(req.Query)
		if req.Query == "" {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: "query is required"})
			return
		}

		registry := deps.GetRegistry()
		cfg := deps.GetConfig()

		scraperNames := req.Scrapers
		if len(scraperNames) == 0 {
			scraperNames = cfg.Scrapers.Priority
		}

		ctx := c.Request.Context()
		var candidates []*models.SearchCandidate
		seenURL := make(map[string]bool)

		for _, name := range scraperNames {
			scraper, found := registry.Get(name)
			if !found || scraper == nil || !scraper.IsEnabled() {
				continue
			}
			cs, isCandidateSearcher := scraper.(models.CandidateSearcher)
			if !isCandidateSearcher {
				continue
			}

			logging.Debugf("SearchCandidates: querying %s for %q", name, req.Query)
			results, err := cs.SearchCandidates(ctx, req.Query)
			if err != nil {
				logging.Warnf("SearchCandidates: %s failed: %v", name, err)
				continue
			}

			for _, r := range results {
				if r == nil || seenURL[r.DetailURL] {
					continue
				}
				seenURL[r.DetailURL] = true
				candidates = append(candidates, r)
			}
		}

		if candidates == nil {
			candidates = []*models.SearchCandidate{}
		}

		c.JSON(http.StatusOK, SearchCandidatesResponse{Candidates: candidates})
	}
}
