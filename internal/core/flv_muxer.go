package core

import (
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"sync"
	"sync/atomic"
	"time"
)

// 用来在子服务器之间传递请求
type flvMuxerHandleRequestReq struct {
	path string
	file string
	ctx  *gin.Context
	res  chan *flvMuxer
}

type flvMuxerParent interface {
	logger.Writer
	muxerClose(*flvMuxer)
}

type flvMuxer struct {
	remoteAddr      string
	segmentCount    int
	segmentDuration conf.StringDuration
	partDuration    conf.StringDuration
	segmentMaxSize  conf.StringSize
	directory       string
	readBufferCount int
	wg              *sync.WaitGroup
	pathName        string
	pathManager     *pathManager
	parent          flvMuxerParent

	lastRequestTime *int64
}

func (m *flvMuxer) handleRequest(ctx *gin.Context) {
	atomic.StoreInt64(m.lastRequestTime, time.Now().UnixNano())
}
