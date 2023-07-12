package core

import (
	"fmt"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"net/http"
)

// 现在只能是照猫画虎了.

type flvHTTPServerParent interface {
	logger.Writer
	handleRequest(req flvMuxerHandleRequestReq)
}

type flvHTTPServer struct {
	pathManager *pathManager
	parent      flvHTTPServerParent
	allowOrigin string

	inner *httpServer
}

// 创建一个新的http-flv服务实例
func newHttpflvServer(
	address string,
	encryption bool,
	serverKey string,
	serverCert string,
	allowOrigin string,
	trustedProxies conf.IPsOrCIDRs,
	readTimeout conf.StringDuration,
	pathManager *pathManager,
	parent flvHTTPServerParent,
) (*flvHTTPServer, error) {
	if encryption {
		if serverCert == "" {
			return nil, fmt.Errorf("server cert is missing")
		}
	} else {
		serverKey = ""
		serverCert = ""
	}

	s := &flvHTTPServer{
		pathManager: pathManager,
		parent:      parent,
		allowOrigin: allowOrigin,
	}

	router := gin.New()
	httpSetTrustedProxies(router, trustedProxies)

	// 需要实现一个服务器的OnRequest功能
	router.NoRoute(httpLoggerMiddleware(s), httpServerHeaderMiddleware, s.onRequest)

	// 创建http-server对象处理请求.
	var err error
	s.inner, err = newHTTPServer(
		address,
		readTimeout,
		serverCert,
		serverKey,
		router,
	)
	if err != nil {
		s.Log(logger.Error, "failed to create inner http server")
		return nil, err
	}

	return s, nil

}

func (s *flvHTTPServer) Log(level logger.Level, format string, args ...interface{}) {
	s.parent.Log(level, format, args...)
}

func (s *flvHTTPServer) close() {
	s.inner.close()
}

func (s *flvHTTPServer) onRequest(ctx *gin.Context) {
	ctx.Writer.Header().Set("Access-Control-Allow-Origin", s.allowOrigin)
	ctx.Writer.Header().Set("Access-Control-Allow-Origin", s.allowOrigin)
	ctx.Writer.Header().Set("Access-Control-Allow-Credentials", "true")

	// 设置一些http头
	switch ctx.Request.Method {
	case http.MethodOptions:
		ctx.Writer.Header().Set("Access-Control-Allow-Methods", "OPTIONS, GET")
		ctx.Writer.Header().Set("Access-Control-Allow-Headers", "Range")
		ctx.Writer.WriteHeader(http.StatusOK)
		return

	case http.MethodGet:

	default:
		return
	}

	// 先返回个Hello world试试?
	// 成功了!!!
	//if _, err := ctx.Writer.WriteString("Hello world!"); err != nil {
	//	s.Log(logger.Error, "failed to write response")
	//}
	// TODO: 下一步就是, 根据用户请求地址, 创建一个flv muxer, 持续往出发flv tag

	s.Log(logger.Debug, "onRequest: %v", ctx.Request.URL.Path)
	//pa := ctx.Request.URL.Path[1:]
	//
	//// 我先试试gin的context能提供什么信息?
	//
	//switch {
	//case pa == "", pa == "favicon.ico":
	//	return
	//
	//case strings.HasSuffix(pa, ".flv"):
	//
	//}
}
