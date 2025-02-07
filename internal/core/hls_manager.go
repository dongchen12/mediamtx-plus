package core

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
)

type hlsManagerAPIMuxersListRes struct {
	data *apiHLSMuxersList
	err  error
}

type hlsManagerAPIMuxersListReq struct {
	res chan hlsManagerAPIMuxersListRes
}

type hlsManagerAPIMuxersGetRes struct {
	data *apiHLSMuxer
	err  error
}

type hlsManagerAPIMuxersGetReq struct {
	name string
	res  chan hlsManagerAPIMuxersGetRes
}

type hlsManagerParent interface {
	logger.Writer
}

type hlsManager struct {
	externalAuthenticationURL string
	alwaysRemux               bool
	variant                   conf.HLSVariant
	segmentCount              int
	segmentDuration           conf.StringDuration
	partDuration              conf.StringDuration
	segmentMaxSize            conf.StringSize
	directory                 string
	readBufferCount           int
	pathManager               *pathManager
	metrics                   *metrics
	parent                    hlsManagerParent

	ctx        context.Context
	ctxCancel  func()
	wg         sync.WaitGroup
	httpServer *hlsHTTPServer
	muxers     map[string]*hlsMuxer

	// in
	chPathSourceReady    chan *path
	chPathSourceNotReady chan *path
	chHandleRequest      chan hlsMuxerHandleRequestReq
	chMuxerClose         chan *hlsMuxer
	chAPIMuxerList       chan hlsManagerAPIMuxersListReq
	chAPIMuxerGet        chan hlsManagerAPIMuxersGetReq
}

func newHLSManager(
	parentCtx context.Context,
	address string,
	encryption bool,
	serverKey string,
	serverCert string,
	externalAuthenticationURL string,
	alwaysRemux bool,
	variant conf.HLSVariant,
	segmentCount int,
	segmentDuration conf.StringDuration,
	partDuration conf.StringDuration,
	segmentMaxSize conf.StringSize,
	allowOrigin string,
	trustedProxies conf.IPsOrCIDRs,
	directory string,
	readTimeout conf.StringDuration,
	readBufferCount int,
	pathManager *pathManager,
	metrics *metrics,
	parent hlsManagerParent,
) (*hlsManager, error) {
	// 创建一个子context
	ctx, ctxCancel := context.WithCancel(parentCtx)

	// 创建一个新的hls实例
	m := &hlsManager{
		externalAuthenticationURL: externalAuthenticationURL,
		alwaysRemux:               alwaysRemux,
		variant:                   variant,
		segmentCount:              segmentCount,
		segmentDuration:           segmentDuration,
		partDuration:              partDuration, // 单个ts碎片时长
		segmentMaxSize:            segmentMaxSize,
		directory:                 directory, // ts碎片保存本地路径
		readBufferCount:           readBufferCount,
		pathManager:               pathManager,
		parent:                    parent,
		metrics:                   metrics,
		ctx:                       ctx,
		ctxCancel:                 ctxCancel,                  // 服务器子模块context cancel
		muxers:                    make(map[string]*hlsMuxer), // hls推流器, 每个流都需要一个muxer
		// 剩下的都是一些用于传递消息的通道
		chPathSourceReady:    make(chan *path),
		chPathSourceNotReady: make(chan *path),
		chHandleRequest:      make(chan hlsMuxerHandleRequestReq),
		chMuxerClose:         make(chan *hlsMuxer),
		chAPIMuxerList:       make(chan hlsManagerAPIMuxersListReq),
		chAPIMuxerGet:        make(chan hlsManagerAPIMuxersGetReq),
	}

	// 创建一个独立的http server提供hls服务
	// TODO: 还是没弄清楚, 到底包是怎么从rtsp那里传过来的...
	var err error
	m.httpServer, err = newHLSHTTPServer(
		address,
		encryption,
		serverKey,
		serverCert,
		allowOrigin,
		trustedProxies,
		readTimeout,
		m.pathManager,
		m,
	)
	if err != nil {
		ctxCancel()
		return nil, err
	}

	m.Log(logger.Info, "listener opened on "+address)

	m.pathManager.hlsManagerSet(m)

	if m.metrics != nil {
		m.metrics.hlsManagerSet(m)
	}

	m.wg.Add(1)
	go m.run()

	return m, nil
}

// Log is the main logging function.
func (m *hlsManager) Log(level logger.Level, format string, args ...interface{}) {
	m.parent.Log(level, "[HLS] "+format, append([]interface{}{}, args...)...)
}

func (m *hlsManager) close() {
	m.Log(logger.Info, "listener is closing")
	m.ctxCancel()
	m.wg.Wait()
}

func (m *hlsManager) run() {
	defer m.wg.Done()

outer:
	for {
		select {
		case pa := <-m.chPathSourceReady:
			if m.alwaysRemux && !pa.conf.SourceOnDemand {
				if _, ok := m.muxers[pa.name]; !ok {
					m.createMuxer(pa.name, "")
				}
			}

		case pa := <-m.chPathSourceNotReady:
			c, ok := m.muxers[pa.name]
			if ok && c.remoteAddr == "" { // created with "always remux"
				c.close()
				delete(m.muxers, pa.name)
			}

		case req := <-m.chHandleRequest:
			r, ok := m.muxers[req.path]
			switch {
			case ok:
				r.processRequest(&req)

			default:
				r := m.createMuxer(req.path, req.ctx.ClientIP())
				r.processRequest(&req)
			}

		case c := <-m.chMuxerClose:
			if c2, ok := m.muxers[c.PathName()]; !ok || c2 != c {
				continue
			}
			delete(m.muxers, c.PathName())

		case req := <-m.chAPIMuxerList:
			data := &apiHLSMuxersList{
				Items: []*apiHLSMuxer{},
			}

			for _, muxer := range m.muxers {
				data.Items = append(data.Items, muxer.apiItem())
			}

			sort.Slice(data.Items, func(i, j int) bool {
				return data.Items[i].Created.Before(data.Items[j].Created)
			})

			req.res <- hlsManagerAPIMuxersListRes{
				data: data,
			}

		case req := <-m.chAPIMuxerGet:
			muxer, ok := m.muxers[req.name]
			if !ok {
				req.res <- hlsManagerAPIMuxersGetRes{err: fmt.Errorf("not found")}
				continue
			}

			req.res <- hlsManagerAPIMuxersGetRes{data: muxer.apiItem()}

		case <-m.ctx.Done():
			break outer
		}
	}

	m.ctxCancel()

	m.httpServer.close()

	m.pathManager.hlsManagerSet(nil)

	if m.metrics != nil {
		m.metrics.hlsManagerSet(nil)
	}
}

func (m *hlsManager) createMuxer(pathName string, remoteAddr string) *hlsMuxer {
	r := newHLSMuxer(
		m.ctx,
		remoteAddr,
		m.externalAuthenticationURL,
		m.variant,
		m.segmentCount,
		m.segmentDuration,
		m.partDuration,
		m.segmentMaxSize,
		m.directory,
		m.readBufferCount,
		&m.wg,
		pathName,
		m.pathManager,
		m)
	m.muxers[pathName] = r
	return r
}

// muxerClose is called by hlsMuxer.
func (m *hlsManager) muxerClose(c *hlsMuxer) {
	select {
	case m.chMuxerClose <- c:
	case <-m.ctx.Done():
	}
}

// pathSourceReady is called by pathManager.
func (m *hlsManager) pathSourceReady(pa *path) {
	select {
	case m.chPathSourceReady <- pa:
	case <-m.ctx.Done():
	}
}

// pathSourceNotReady is called by pathManager.
func (m *hlsManager) pathSourceNotReady(pa *path) {
	select {
	case m.chPathSourceNotReady <- pa:
	case <-m.ctx.Done():
	}
}

// apiMuxersList is called by api.
func (m *hlsManager) apiMuxersList() (*apiHLSMuxersList, error) {
	req := hlsManagerAPIMuxersListReq{
		res: make(chan hlsManagerAPIMuxersListRes),
	}

	select {
	case m.chAPIMuxerList <- req:
		res := <-req.res
		return res.data, res.err

	case <-m.ctx.Done():
		return nil, fmt.Errorf("terminated")
	}
}

// apiMuxersGet is called by api.
func (m *hlsManager) apiMuxersGet(name string) (*apiHLSMuxer, error) {
	req := hlsManagerAPIMuxersGetReq{
		name: name,
		res:  make(chan hlsManagerAPIMuxersGetRes),
	}

	select {
	case m.chAPIMuxerGet <- req:
		res := <-req.res
		return res.data, res.err

	case <-m.ctx.Done():
		return nil, fmt.Errorf("terminated")
	}
}

func (m *hlsManager) handleRequest(req hlsMuxerHandleRequestReq) {
	req.res = make(chan *hlsMuxer) // 创建一个hls chan.

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
