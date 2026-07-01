package api

import (
	"rc_stewarthuang/internal/db"

	"github.com/gin-gonic/gin"
)

type App struct {
	Store  *db.Store
	Router *gin.Engine
}

func NewApp(store *db.Store) *App {
	app := &App{Store: store}
	router := gin.Default()

	v1 := router.Group("/api/v1")
	{
		suppliers := v1.Group("/suppliers")
		{
			suppliers.GET("", app.ListSuppliers)
			suppliers.GET("/:name", app.GetSupplier)
			suppliers.POST("", app.CreateSupplier)
			suppliers.PUT("/:name", app.UpdateSupplier)
			suppliers.DELETE("/:name", app.DeleteSupplier)
		}

		notifications := v1.Group("/notifications")
		{
			notifications.POST("", app.SubmitNotification)
			notifications.GET("/:id", app.GetNotification)
			notifications.GET("", app.ListNotifications)
			notifications.POST("/:id/replay", app.ReplayDeadLetter)
		}
	}

	app.Router = router
	return app
}
