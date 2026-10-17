package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/gin-gonic/gin"
)

func SetTokenHubPortraitRouter(router *gin.Engine) {
	r := router.Group("/v1/portrait", middleware.RouteTag("relay"), middleware.TokenAuth())
	r.POST("/asset-group", controller.TokenHubPortrait)
	r.POST("/asset-group/list", controller.TokenHubPortrait)
	r.GET("/asset-group/:id", controller.TokenHubPortrait)
	r.PUT("/asset-group/:id", controller.TokenHubPortrait)
	r.DELETE("/asset-group/:id", controller.TokenHubPortrait)
	r.POST("/asset", controller.TokenHubPortrait)
	r.POST("/asset/list", controller.TokenHubPortrait)
	r.GET("/asset/:id", controller.TokenHubPortrait)
	r.DELETE("/asset/:id", controller.TokenHubPortrait)
}
