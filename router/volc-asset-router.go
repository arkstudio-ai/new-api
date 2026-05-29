package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

// SetVolcAssetRouter 注册火山方舟 Asset（素材资产）管理接口。
// 对外兼容火山原生 /open/<Action> 风格，使用 new-api Bearer 令牌鉴权；
// 不挂 Distribute（Asset 无 model 字段），渠道由 controller 按令牌分组选取。
func SetVolcAssetRouter(router *gin.Engine) {
	openRouter := router.Group("/open")
	openRouter.Use(middleware.RouteTag("relay"))
	openRouter.Use(middleware.TokenAuth())
	{
		openRouter.POST("/CreateAssetGroup", controller.VolcCreateAssetGroup)
		openRouter.POST("/CreateAsset", controller.VolcCreateAsset)
		openRouter.POST("/ListAssetGroups", controller.VolcListAssetGroups)
		openRouter.POST("/ListAssets", controller.VolcListAssets)
		openRouter.POST("/GetAsset", controller.VolcGetAsset)
		openRouter.POST("/GetAssetGroup", controller.VolcGetAssetGroup)
		openRouter.POST("/UpdateAssetGroup", controller.VolcUpdateAssetGroup)
		openRouter.POST("/UpdateAsset", controller.VolcUpdateAsset)
	}
}
