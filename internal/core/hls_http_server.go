package core

import (
	_ "embed"
	"fmt"
	"net"
	"net/http"
	gopath "path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
)

//go:embed hls_index.html
var hlsIndex []byte

type hlsHTTPServerParent interface {
	logger.Writer
	handleRequest(req hlsMuxerHandleRequestReq)
}

type hlsHTTPServer struct {
	allowOrigin string
	pathManager *pathManager
	parent      hlsHTTPServerParent

	inner *httpServer
}

func newHLSHTTPServer( //nolint:dupl
	address string,
	encryption bool,
	serverKey string,
	serverCert string,
	allowOrigin string,
	trustedProxies conf.IPsOrCIDRs,
	readTimeout conf.StringDuration,
	pathManager *pathManager,
	parent hlsHTTPServerParent,
) (*hlsHTTPServer, error) {
	// 认证
	if encryption {
		if serverCert == "" {
			return nil, fmt.Errorf("server cert is missing")
		}
	} else {
		serverKey = ""
		serverCert = ""
	}

	// 创建Server对象
	s := &hlsHTTPServer{
		allowOrigin: allowOrigin,
		pathManager: pathManager,
		parent:      parent,
	}

	router := gin.New()
	httpSetTrustedProxies(router, trustedProxies)

	// 这里似乎会给服务器注册一个路由为空时的处理函数.
	router.NoRoute(httpLoggerMiddleware(s), httpServerHeaderMiddleware, s.onRequest)

	var err error
	s.inner, err = newHTTPServer(
		address,
		readTimeout,
		serverCert,
		serverKey,
		router,
	)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func (s *hlsHTTPServer) Log(level logger.Level, format string, args ...interface{}) {
	s.parent.Log(level, format, args...)
}

func (s *hlsHTTPServer) close() {
	s.inner.close()
}

func (s *hlsHTTPServer) onRequest(ctx *gin.Context) {
	//log.Println("Function onRequest invoked...")
	ctx.Writer.Header().Set("Access-Control-Allow-Origin", s.allowOrigin)
	ctx.Writer.Header().Set("Access-Control-Allow-Credentials", "true")

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

	//log.Println("Initial URL path:", ctx.Request.URL.Path)
	// remove leading prefix '/'
	pa := ctx.Request.URL.Path[1:]

	//log.Println("Content of pa:" + pa)

	var dir string
	var fname string

	// 获取流名称, 获取m3u8索引文件名称.
	switch {
	case pa == "", pa == "favicon.ico":
		return

	case strings.HasSuffix(pa, ".m3u8") ||
		strings.HasSuffix(pa, ".ts") ||
		strings.HasSuffix(pa, ".mp4") ||
		strings.HasSuffix(pa, ".mp"):
		dir, fname = gopath.Dir(pa), gopath.Base(pa)
		//log.Println("Dir is:", dir, ", fname is:", fname)

		if strings.HasSuffix(fname, ".mp") {
			fname += "4"
		} // 把mp尾缀改成mp4

	default:
		dir, fname = pa, ""

		if !strings.HasSuffix(dir, "/") {
			l := "/" + dir + "/"
			if ctx.Request.URL.RawQuery != "" {
				l += "?" + ctx.Request.URL.RawQuery
			}
			ctx.Writer.Header().Set("Location", l)
			ctx.Writer.WriteHeader(http.StatusMovedPermanently)
			return
		}
	}

	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		return
	}

	user, pass, hasCredentials := ctx.Request.BasicAuth()

	// 这里似乎是用来验证的, 如果我不需要验证怎么办呢?
	res := s.pathManager.getPathConf(pathGetPathConfReq{
		name:    dir,
		publish: false,
		credentials: authCredentials{
			query: ctx.Request.URL.RawQuery,
			ip:    net.ParseIP(ctx.ClientIP()),
			user:  user,
			pass:  pass,
			proto: authProtocolWebRTC,
		},
	})
	if res.err != nil {
		if terr, ok := res.err.(pathErrAuth); ok {
			if !hasCredentials {
				ctx.Header("WWW-Authenticate", `Basic realm="mediamtx"`)
				ctx.Writer.WriteHeader(http.StatusUnauthorized)
				return
			}

			s.Log(logger.Info, "authentication error: %v", terr.wrapped)
			ctx.Writer.WriteHeader(http.StatusUnauthorized)
			return
		}

		ctx.Writer.WriteHeader(http.StatusNotFound)
		return
	}
	switch fname {
	case "":
		ctx.Writer.Header().Set("Content-Type", "text/html")
		ctx.Writer.WriteHeader(http.StatusOK)
		ctx.Writer.Write(hlsIndex)

	default:
		s.parent.handleRequest(hlsMuxerHandleRequestReq{ // 发送一个hls处理请求
			path: dir,
			file: fname,
			ctx:  ctx,
		})
	}
}
