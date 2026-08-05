package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *APIServer) getFlightSearch(c *gin.Context) {
	params, err := parseFlightSearchParams(c.Request.URL.Query(), time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	where, args := buildFlightSearchWhere(params)
	orderBy := flightSearchOrderBy(params)

	var total int
	countQuery := "SELECT COUNT(*) " + flightSearchBaseQuery + where
	if err := s.pg.db.QueryRow(context.Background(), countQuery, args...).Scan(&total); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	offset := (params.Page - 1) * params.PageSize
	dataArgs := append(append([]any{}, args...), params.PageSize, offset)
	dataQuery := fmt.Sprintf(
		"SELECT %s %s %s ORDER BY %s LIMIT $%d OFFSET $%d",
		flightSearchSelectColumns, flightSearchBaseQuery, where, orderBy,
		len(args)+1, len(args)+2,
	)

	rows, err := s.pg.db.Query(context.Background(), dataQuery, dataArgs...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	results := []gin.H{}
	for rows.Next() {
		r, err := scanFlightSearchRow(rows)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		results = append(results, flightSearchRowToJSON(r))
	}

	c.JSON(http.StatusOK, gin.H{
		"results":     results,
		"total_count": total,
		"page":        params.Page,
		"page_size":   params.PageSize,
	})
}
