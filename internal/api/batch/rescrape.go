package batch

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/javinizer/javinizer-go/internal/logging"
	"github.com/javinizer/javinizer-go/internal/models"
	"github.com/javinizer/javinizer-go/internal/worker"
)

// bustTempPosterCache appends a cache-busting query param to a temp poster URL.
// A rescrape overwrites the cropped poster file at the same path, so without
// this the browser keeps serving the cached image until a full page refresh.
// The temp poster route matches by filename and ignores the query string, so
// this only affects browser caching. Mirrors the behaviour of the poster-crop
// and poster-from-URL endpoints in movie_edit.go.
func bustTempPosterCache(rawURL string) string {
	if rawURL == "" || strings.Contains(rawURL, "v=") {
		return rawURL
	}
	separator := "?"
	if strings.Contains(rawURL, "?") {
		separator = "&"
	}
	return fmt.Sprintf("%s%sv=%d", rawURL, separator, time.Now().UnixMilli())
}

// applyPosterCacheBusting refreshes the cache-busting param on a rescraped
// movie's temp poster URLs so the review UI shows the new image immediately.
func applyPosterCacheBusting(movie *models.Movie) {
	if movie == nil {
		return
	}
	movie.CroppedPosterURL = bustTempPosterCache(movie.CroppedPosterURL)
	if strings.Contains(movie.PosterURL, "/api/v1/temp/posters/") {
		movie.PosterURL = bustTempPosterCache(movie.PosterURL)
	}
}

// rescrapeBatchMovie godoc
// @Summary Rescrape movie in batch job
// @Description Rescrape a specific movie within a batch job using selected scrapers or manual search input
// @Tags web
// @Accept json
// @Produce json
// @Param id path string true "Job ID"
// @Param movieId path string true "Movie ID"
// @Param request body BatchRescrapeRequest true "Rescrape options"
// @Success 200 {object} BatchRescrapeResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 410 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/batch/{id}/movies/{movieId}/rescrape [post]
func rescrapeBatchMovie(deps *ServerDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		jobID := c.Param("id")
		movieID := c.Param("movieId")

		var req BatchRescrapeRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}

		if httpStatus, errMsg := validateRescrapeRequest(&req); errMsg != "" {
			writeErrorResponse(c, httpStatus, false, errMsg)
			return
		}

		logging.Infof("Batch rescrape request for job %s, movie %s: scrapers=%v, manual_input=%s, force=%v",
			jobID, movieID, req.SelectedScrapers, req.ManualSearchInput, req.Force)

		job, ok := deps.JobQueue.GetJobPointer(jobID)
		if !ok {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "Job not found"})
			return
		}

		isGone, httpStatus, errMsg := validateJobState(job)
		if errMsg != "" {
			writeErrorResponse(c, httpStatus, isGone, errMsg)
			return
		}

		lookup, httpStatus, errMsg := findFileForMovieID(job, movieID)
		if errMsg != "" {
			c.JSON(httpStatus, ErrorResponse{Error: errMsg})
			return
		}

		cfg := deps.GetConfig()

		// Check if job was deleted during rescrape (before starting work)
		job.Lock()
		if job.IsDeleted() {
			job.Unlock()
			writeErrorResponse(c, http.StatusGone, true, "Job has been deleted")
			return
		}
		job.Unlock()

		params, _ := resolveScrapeParams(&req, movieID, deps)

		result, err := executeRescrape(c.Request.Context(), params, job, lookup.foundFilePath, deps, &req, cfg)
		if err != nil {
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("Rescrape failed: %v", err)})
			return
		}

		if result == nil {
			logging.Errorf("[Rescrape] RunBatchScrapeOnce returned nil result for %s", lookup.foundFilePath)
			c.JSON(http.StatusInternalServerError, ErrorResponse{Error: "Rescrape produced no result"})
			return
		}

		if result.Status != worker.JobStatusCompleted {
			errorMsg := "Unknown error"
			if result.Error != "" {
				errorMsg = result.Error
			}
			c.JSON(http.StatusUnprocessableEntity, ErrorResponse{Error: fmt.Sprintf("Rescrape failed: %s", errorMsg)})
			return
		}

		// Get movie from result data for response and poster cleanup
		var movie *models.Movie
		if result.Data != nil {
			if m, ok := result.Data.(*models.Movie); ok {
				movie = m
			}
		}

		// Refresh the cache-busting param on the temp poster URLs before the movie
		// is stored in job state, so the review UI loads the new image immediately
		// (the rescrape overwrites the poster file at the same path).
		applyPosterCacheBusting(movie)

		updateRes := validateAndUpdateResult(job, result, lookup.foundFilePath, lookup.capturedRevision, movie, lookup.oldMovieID, cfg, jobID)
		if updateRes.shouldAbort {
			writeErrorResponse(c, updateRes.httpStatus, updateRes.isGone, updateRes.errorMessage)
			return
		}

		cleanupPosterPaths(updateRes.posterPaths)
		deps.JobQueue.PersistJob(job)

		logging.Infof("[Rescrape] Verified update for %s: movieID=%s, status=%s",
			lookup.foundFilePath, result.MovieID, result.Status)

		c.JSON(http.StatusOK, BatchRescrapeResponse{
			Movie:          movie,
			FieldSources:   result.FieldSources,
			ActressSources: result.ActressSources,
		})
	}
}
