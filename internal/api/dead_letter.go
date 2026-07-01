package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (a *App) ReplayDeadLetter(c *gin.Context) {
	if err := a.Store.ReplayNotification(c.Param("id")); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"status": "replayed"})
}
