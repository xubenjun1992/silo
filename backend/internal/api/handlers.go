package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/silo-protocol/backend/internal/service"
)

var svc = service.NewPoolService()

// GET /api/v1/pools
func listPools(c *gin.Context) {
	pools, err := svc.ListPools()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"pools": pools})
}

// GET /api/v1/pools/:addr
func getPool(c *gin.Context) {
	addr := c.Param("addr")
	stats, err := svc.GetPoolStats(addr)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pool not found"})
		return
	}
	c.JSON(http.StatusOK, stats)
}

// GET /api/v1/pools/:addr/events
func getPoolEvents(c *gin.Context) {
	addr := c.Param("addr")
	events, err := svc.GetEvents(addr, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"address": addr, "events": events})
}
