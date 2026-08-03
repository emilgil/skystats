package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog/log"
)

// watchOperatorLabels gives the frontend a readable name per operator without
// duplicating the vocabulary on the client.
var watchOperatorLabels = map[string]string{
	"equals":      "is",
	"contains":    "contains",
	"starts_with": "starts with",
	"over":        "is over",
	"under":       "is under",
	"in_list":     "is any of",
	"is_true":     "is true",
}

// WatchHit is one row of the hit history.
type WatchHit struct {
	ID             int            `json:"id"`
	WatchID        *int           `json:"watch_id"`
	WatchName      string         `json:"watch_name"`
	Hex            string         `json:"hex"`
	Flight         *string        `json:"flight"`
	Registration   *string        `json:"registration"`
	Snapshot       map[string]any `json:"snapshot"`
	NotifiedAt     time.Time      `json:"notified_at"`
	AppriseSuccess bool           `json:"apprise_success"`
	AppriseError   *string        `json:"apprise_error"`
}

// watchPayload is the request body for create and update. Conditions replace
// the watch's full condition list.
type watchPayload struct {
	Name       string           `json:"name"`
	Enabled    *bool            `json:"enabled"`
	Combinator string           `json:"combinator"`
	AppriseKey string           `json:"apprise_key"`
	Conditions []WatchCondition `json:"conditions"`
}

func (p watchPayload) toWatch() Watch {
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	combinator := p.Combinator
	if combinator == "" {
		combinator = "AND"
	}
	conditions := p.Conditions
	if conditions == nil {
		conditions = []WatchCondition{}
	}
	return Watch{
		Name:       p.Name,
		Enabled:    enabled,
		Combinator: combinator,
		AppriseKey: p.AppriseKey,
		Conditions: conditions,
	}
}

func (s *APIServer) getWatchFields(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"fields":    watchFieldList(),
		"operators": watchOperatorLabels,
	})
}

func (s *APIServer) getWatches(c *gin.Context) {
	watches, err := listWatches(s.pg)
	if err != nil {
		log.Error().Err(err).Msg("getWatches() - query failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to load watches"})
		return
	}
	c.JSON(http.StatusOK, watches)
}

func (s *APIServer) createWatchHandler(c *gin.Context) {

	var payload watchPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	watch := payload.toWatch()
	if err := validateWatch(watch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	created, err := createWatch(s.pg, watch)
	if err != nil {
		log.Error().Err(err).Msg("createWatchHandler() - insert failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to create watch"})
		return
	}
	c.JSON(http.StatusCreated, created)
}

func (s *APIServer) updateWatchHandler(c *gin.Context) {

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid watch id"})
		return
	}

	var payload watchPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	watch := payload.toWatch()
	if err := validateWatch(watch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updated, err := updateWatch(s.pg, id, watch)
	if err != nil {
		log.Error().Err(err).Msg("updateWatchHandler() - update failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to update watch"})
		return
	}
	if updated == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "watch not found"})
		return
	}
	c.JSON(http.StatusOK, updated)
}

func (s *APIServer) deleteWatchHandler(c *gin.Context) {

	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid watch id"})
		return
	}

	if err := deleteWatch(s.pg, id); err != nil {
		if err == pgx.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "watch not found"})
			return
		}
		log.Error().Err(err).Msg("deleteWatchHandler() - delete failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to delete watch"})
		return
	}

	c.Status(http.StatusNoContent)
}

func (s *APIServer) getWatchHits(c *gin.Context) {

	limit := 50
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 500 {
		limit = v
	}
	offset := 0
	if v, err := strconv.Atoi(c.Query("offset")); err == nil && v > 0 {
		offset = v
	}

	watchID := 0
	if v, err := strconv.Atoi(c.Query("watch_id")); err == nil && v > 0 {
		watchID = v
	}

	rows, err := s.pg.db.Query(context.Background(), `
		SELECT id, watch_id, watch_name, hex, flight, registration, snapshot,
		       notified_at, apprise_success, apprise_error
		FROM watch_notifications
		WHERE ($1 = 0 OR watch_id = $1)
		ORDER BY notified_at DESC, id DESC
		LIMIT $2 OFFSET $3`, watchID, limit, offset)
	if err != nil {
		log.Error().Err(err).Msg("getWatchHits() - query failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unable to load watch hits"})
		return
	}
	defer rows.Close()

	hits := []WatchHit{}
	for rows.Next() {
		var h WatchHit
		if err := rows.Scan(&h.ID, &h.WatchID, &h.WatchName, &h.Hex, &h.Flight,
			&h.Registration, &h.Snapshot, &h.NotifiedAt, &h.AppriseSuccess, &h.AppriseError); err != nil {
			log.Error().Err(err).Msg("getWatchHits() - error scanning rows")
			continue
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		log.Error().Err(err).Msg("getWatchHits() - row iteration failed")
	}

	c.JSON(http.StatusOK, gin.H{"hits": hits})
}
