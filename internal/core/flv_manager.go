package core

import (
	"context"
	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
	"net/http"
	"sync"
)

type flvManagerParent interface {
	logger.Writer
}

// 这些结构体都是用来在子服务器之间传递请求的, 做API查看服务器状态的时候可以用一下
type flvManagerAPIMuxersListRes struct {
}

type flvManagerAPIMuxersListReq struct {
}

type flvManagerAPIMuxersGetRes struct {
}

type flvManagerAPIMuxersGetReq struct {
}

// 需要将flv格式的视频流数据按照一定的分片大小或时间间隔进行传输, 客户端通过解析响应体中的FLV Tag, 逐步获取并播放视频数据.
type flvManager struct {
	segmentCount    int
	segmentDuration conf.StringDuration
	partDuration    conf.StringDuration
	segmentMaxSize  conf.StringSize
	pathManager     *pathManager
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
	chAPIMuxerList       chan flvManagerAPIMuxersListReq
	chAPIMuxerGet        chan flvManagerAPIMuxersGetReq
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
	readBufferCount int,
	pathManager *pathManager,
	parent hlsManagerParent,
) (*flvManager, error) {
	// 创建子context
	ctx, ctxCancel := context.WithCancel(parentCtx)

	m := &flvManager{
		ctx:             ctx,
		ctxCancel:       ctxCancel,
		parent:          parent,
		pathManager:     pathManager,
		readBufferCount: readBufferCount,
		muxers:          make(map[string]*flvMuxer),

		chPathSourceReady:    make(chan *path),
		chPathSourceNotReady: make(chan *path),
		chHandleRequest:      make(chan flvMuxerHandleRequestReq),
		chMuxerClose:         make(chan *flvMuxer),
		chAPIMuxerGet:        make(chan flvManagerAPIMuxersGetReq),
		chAPIMuxerList:       make(chan flvManagerAPIMuxersListReq),
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

	m.wg.Add(1)

	go m.run()

	return m, nil
}

func (m *flvManager) run() {
	defer m.wg.Done()

outerlabel:
	for {
		select {
		case pa := <-m.chPathSourceReady:
			m.Log(logger.Info, "in flv manager path source ready: %s", pa.name)
			if _, ok := m.muxers[pa.name]; !ok {
				m.createMuxer(pa.name, "")
			}

		case <-m.ctx.Done():
			m.Log(logger.Info, "listener is closing")
			return

		case req := <-m.chHandleRequest: // 处理
			m.Log(logger.Info, "received ch signal from flv manager handle request, sname: ", req.sname)
			if muxer, ok := m.muxers[req.sname]; ok {
				m.Log(logger.Info, "flv muxer: "+req.sname+" ready, processing request...")
				muxer.processRequest(&req)
			} else {
				m.Log(logger.Info, "flv muxer not found, sname: "+req.sname+", create new one")
				muxer := m.createMuxer(req.sname, req.ctx.ClientIP()) // 为请求用户创建一个新的Muxer
				muxer.processRequest(&req)
			}

		case muxer := <-m.chMuxerClose:
			muxer.close()

		case <-m.chAPIMuxerList:
			break outerlabel
		}
	}

	m.ctxCancel()

	m.httpServer.close()

	m.pathManager.flvManagerSet(nil)
}

// 创建一个新的Muxer
func (m *flvManager) createMuxer(pathName string, remoteAddr string) *flvMuxer {
	muxer := newFlvMuxer(m.ctx, remoteAddr, m.segmentCount, m.readBufferCount, &m.wg, pathName, m.pathManager, m)
	m.muxers[pathName] = muxer
	return muxer
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

func (m *flvManager) muxerClose(muxer *flvMuxer) {
	select {
	case m.chMuxerClose <- muxer:
	case <-m.ctx.Done():
	}
}

// 处理来自flvHttpServer的请求, 负责找muxer处理请求
func (m *flvManager) handleRequest(req flvMuxerHandleRequestReq) {
	req.res = make(chan *flvMuxer)

	select {
	case m.chHandleRequest <- req: // 需要处理的请求
		muxer := <-req.res // 收到对应的muxer会放在这个chan中
		if muxer != nil {
			muxer.handleRequest(req.ctx)
		} else {
			req.ctx.AbortWithStatus(http.StatusNotFound)
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
