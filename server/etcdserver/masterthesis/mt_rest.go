package masterthesis

import (
	"context"
)

//
//import (
//	"github.com/gin-gonic/gin"
//	"net/http"
//)
//
//func RunConfigApi() *http.Server {
//	router := gin.Default()
//	router.GET("/config/get", getConfig)
//	router.POST("/config/update", updateConfig)
//	httpServer := http.Server{Addr: ":8090", Handler: router}
//	go func() {
//		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
//			panic(err)
//		}
//	}()
//
//	return &httpServer
//}
//
//func getConfig(context *gin.Context) {
//	config := &DaActionPicker.Load().faultConfig
//	context.YAML(200, config)
//}
//
//func updateConfig(context *gin.Context) {
//	var config FaultConfig
//	if err := context.BindYAML(&config); err != nil {
//		DaLogger.ErrorErr(err, "Could not read config update entity")
//		_ = context.AbortWithError(400, err)
//		return
//	}
//
//	if err := config.verifyConfig(); err != nil {
//		DaLogger.ErrorErr(err, "Failed to verify received config")
//		_ = context.AbortWithError(400, err)
//		return
//	}
//
//	DaActionPicker.Store(NewActionPicker(config))
//	context.Status(200)
//}

type Temp struct {
}

func (t *Temp) Shutdown(ctx context.Context) error {
	return nil
}

func RunConfigApi() *Temp {
	return &Temp{}
}
