package api

import (
	"github.com/gin-gonic/gin"
	"github.com/silo-protocol/backend/internal/config"
)

func NewRouter(cfg *config.Config) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	api := r.Group("/api/v1")
	{
		api.GET("/pools", listPools)
		api.GET("/pools/:addr", getPool)
		api.GET("/pools/:addr/events", getPoolEvents)
		api.GET("/health", healthCheck)
	}

	return r
}

func healthCheck(c *gin.Context) {
	c.JSON(200, gin.H{"status": "ok", "protocol": "silo"})
}
