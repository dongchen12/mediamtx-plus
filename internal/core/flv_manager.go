package core

import (
	"context"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"sync"
)

type flvManagerParent interface {
	logger.Writer
}

// 需要将flv格式的视频流数据按照一定的分片大小或时间间隔进行传输, 客户端通过解析响应体中的FLV Tag, 逐步获取并播放视频数据.
type flvManager struct {
	segmentCount    int
	segmentDuration conf.StringDuration
	partDuration    conf.StringDuration
	segmentMaxSize  conf.StringSize
	pathManager     *pathManager
	directory       string
	readBufferCount int
	parent          flvManagerParent

	ctx        context.Context
	ctxCancel  func()
	wg         sync.WaitGroup
	httpServer *flvHTTPServer
	muxers     map[string]*flvMuxer // 这个Muxer就是一个主要的把视频片段发出去东西, 这里和流标识符对应起来

	// 一些用于异步通讯的chan
	chPathSourceReady    chan *path // 获取RTSP资源的通道
	chPathSourceNotReady chan *path
	chHandleRequest      chan flvMuxerHandleRequestReq
	chMuxerClose         chan *flvMuxer
	//chAPIMuxerList       chan flvManagerAPIMuxersListReq
	//chAPIMuxerGet        chan flvManagerAPIMuxersGetReq
}

func newFlvManager(
	parentCtx context.Context,
	address string,
	encryption bool,
	serverKey string,
	serverCert string,
	allowOrigin string,
	trustedProxies conf.IPsOrCIDRs,
	readTimeout conf.StringDuration,
	writeTimeout conf.StringDuration,
	readBufferCount int,
	pathManager *pathManager,
	parent hlsManagerParent,
) (*flvManager, error) {
	// 创建子context
	ctx, ctxCancel := context.WithCancel(parentCtx)

	// TODO 实现run之后把这个删掉!
	defer ctxCancel()

	m := &flvManager{
		ctx:         ctx,
		parent:      parent,
		pathManager: pathManager,
	}

	var err error
	m.httpServer, err = newHttpflvServer(address,
		encryption,
		serverKey,
		serverCert,
		allowOrigin,
		trustedProxies,
		readTimeout,
		m.pathManager,
		m)
	if err != nil {
		ctxCancel()
		return nil, err
	}

	m.Log(logger.Info, "listener opened on "+address)

	m.pathManager.flvManagerSet(m)

	// metrics暂时先不管了

	m.wg.Add(1)

	//go m.run()

	return m, nil
}

func (m *flvManager) run() {

}

// Log is the main logging function.
func (m *flvManager) Log(level logger.Level, format string, args ...interface{}) {
	m.parent.Log(level, "[HTTP-flv] "+format, append([]interface{}{}, args...)...)
}

func (m *flvManager) close() {
	m.Log(logger.Info, "listener is closing")
	m.ctxCancel()
	m.wg.Wait()
}

func (m *flvManager) handleRequest(req flvMuxerHandleRequestReq) {
	req.res = make(chan *flvMuxer)

	select {
	case m.chHandleRequest <- req: // 把需要处理的请求放进这个chan中
		muxer := <-req.res // 处理的结果会放在这个chan中
		if muxer != nil {
			req.ctx.Request.URL.Path = req.file
			muxer.handleRequest(req.ctx)
		}

	case <-m.ctx.Done():
	}

}

// pathSourceReady is called by pathManager.
func (m *flvManager) pathSourceReady(pa *path) {
	select {
	case m.chPathSourceReady <- pa:
	case <-m.ctx.Done():
	}
}

// pathSourceNotReady is called by pathManager.
func (m *flvManager) pathSourceNotReady(pa *path) {
	select {
	case m.chPathSourceNotReady <- pa:
	case <-m.ctx.Done():
	}
}
