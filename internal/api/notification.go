package api

import "github.com/gin-gonic/gin"

func (a *App) SubmitNotification(c *gin.Context) { c.JSON(501, gin.H{"error": "not implemented"}) }
func (a *App) GetNotification(c *gin.Context)     { c.JSON(501, gin.H{"error": "not implemented"}) }
func (a *App) ListNotifications(c *gin.Context)   { c.JSON(501, gin.H{"error": "not implemented"}) }
func (a *App) ReplayDeadLetter(c *gin.Context)    { c.JSON(501, gin.H{"error": "not implemented"}) }
