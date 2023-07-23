package core

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
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

	innerMonitor *contextMonitor
	inner        *httpServer
}

func newInnerHttpServer(
	address string,
	serverCert string,
	serverKey string,
	handler http.Handler,
) (*httpServer, error) {
	ln, err := net.Listen(restrictNetwork("tcp", address))
	if err != nil {
		return nil, err
	}
	//
	//test_router := gin.Default()
	//test_router.GET("/:sname/video.flv", func(context *gin.Context) {
	//	sname := context.Param("sname")
	//	log.Default().Println("sname: " + sname)
	//	for i := 0; i < 5; i++ {
	//		_, err := context.Writer.WriteString("hello world")
	//		if err != nil {
	//			log.Default().Println("failed to write string")
	//			return
	//		}
	//	}
	//})
	//test_router.Run(":9988")

	var tlsConfig *tls.Config
	if serverCert != "" {
		crt, err := tls.LoadX509KeyPair(serverCert, serverKey)
		if err != nil {
			ln.Close()
			return nil, err
		}

		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{crt},
		}
	}

	s := &httpServer{
		ln: ln,
		inner: &http.Server{
			Handler:           handler,
			TLSConfig:         tlsConfig,
			ReadHeaderTimeout: 10 * time.Second,
			ErrorLog:          log.New(&nilWriter{}, "", 0),
		},
	}

	if tlsConfig != nil {
		go s.inner.ServeTLS(s.ln, "", "")
	} else {
		go s.inner.Serve(s.ln)
	}

	return s, nil
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

	s.innerMonitor = newContextMonitor(s)
	s.innerMonitor.run(context.Background())

	router := gin.Default()
	//httpSetTrustedProxies(router, trustedProxies)
	router.NoRoute(s.onRequest)

	// 需要实现一个服务器的OnRequest功能
	//router.NoRoute(httpLoggerMiddleware(s), httpServerHeaderMiddleware, s.onRequest)

	// TODO: 检查timeout
	var err error
	s.inner, err = newInnerHttpServer(
		address,
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

// 回调函数 用于处理请求
func (s *flvHTTPServer) onRequest(ctx *gin.Context) {
	ctx.Header("Access-Control-Allow-Origin", s.allowOrigin)
	ctx.Header("Access-Control-Allow-Credentials", "true")
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

	pa := ctx.Request.URL.Path[1:]

	var sname string

	switch {
	case strings.HasSuffix(pa, ".flv") && !strings.ContainsAny(pa, "/"):
		sname = pa[:len(pa)-4]

	default:
		ctx.Writer.WriteHeader(http.StatusNotFound)
		return
	}

	//user, pass, hasCredentials := ctx.Request.BasicAuth()
	//
	//res := s.pathManager.getPathConf(pathGetPathConfReq{
	//	name:    sname,
	//	publish: false,
	//	credentials: authCredentials{
	//		query: ctx.Request.URL.RawQuery,
	//		ip:    net.ParseIP(ctx.ClientIP()),
	//		user:  user,
	//		pass:  pass,
	//		proto: authProtocolWebRTC,
	//	},
	//})
	//if res.err != nil {
	//	if terr, ok := res.err.(pathErrAuth); ok {
	//		if !hasCredentials {
	//			ctx.Header("WWW-Authenticate", `Basic realm="mediamtx"`)
	//			ctx.Writer.WriteHeader(http.StatusUnauthorized)
	//			return
	//		}
	//
	//		s.Log(logger.Info, "authentication error: %v", terr.wrapped)
	//		ctx.Writer.WriteHeader(http.StatusUnauthorized)
	//		return
	//	}
	//
	//	ctx.Writer.WriteHeader(http.StatusNotFound)
	//	return
	//}

	//s.Log(logger.Info, "before handleRequest")
	// 把这个请求丢给对应的flvMultiplexer处理
	s.parent.handleRequest(flvMuxerHandleRequestReq{ // 转发flv处理请求, 创建一个flvWriter
		sname: sname,
		ctx:   ctx,
	})

}
