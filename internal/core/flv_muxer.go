package core

import (
	"context"
	"errors"
	"fmt"
	"github.com/bluenviron/gortsplib/v3/pkg/formats"
	"github.com/bluenviron/mediamtx/internal/formatprocessor"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/pion/rtp"
	"sync"
	"sync/atomic"
	"time"
)

// 用来在子服务器之间传递请求
type flvMuxerHandleRequestReq struct {
	sname      string
	ctx        *gin.Context
	res        chan *flvMuxer
	timer      *time.Timer
	reqHandled chan struct{}
}

type flvMuxerParent interface {
	logger.Writer
	muxerClose(*flvMuxer)
}

type flvMuxer struct {
	remoteAddr      string
	segmentCount    int
	directory       string
	readBufferCount int
	wg              *sync.WaitGroup
	pathName        string
	pathManager     *pathManager
	parent          flvMuxerParent

	ctx             context.Context
	ctxCancel       func()
	created         time.Time
	path            *path
	requests        []*flvMuxerHandleRequestReq
	bytesSent       *uint64
	lastRequestTime *int64
	writers         []*FlvWriter
	packetBuffer    sync.Map

	chRequest chan *flvMuxerHandleRequestReq
}

func newFlvMuxer(
	parentCtx context.Context,
	remoteAddr string,
	segmentCount int,
	readBufferCount int,
	wg *sync.WaitGroup,
	pathName string,
	pathManager *pathManager,
	parent flvMuxerParent,
) *flvMuxer {
	ctx, ctxCancel := context.WithCancel(parentCtx)

	m := &flvMuxer{
		remoteAddr:      remoteAddr,
		segmentCount:    segmentCount,
		readBufferCount: readBufferCount,
		wg:              wg,
		pathName:        pathName,
		pathManager:     pathManager,
		parent:          parent,
		lastRequestTime: int64Ptr(time.Now().UnixNano()),
		ctx:             ctx,
		ctxCancel:       ctxCancel,
		created:         time.Now(),
		bytesSent:       new(uint64),
		chRequest:       make(chan *flvMuxerHandleRequestReq),
	}
	var err error

	if err != nil {
		return nil
	}

	m.Log(logger.Info, "created %s", func() string {
		if remoteAddr == "" {
			return "automatically"
		}
		return "(requested by " + remoteAddr + ")"
	}())

	m.wg.Add(1)
	go m.run()

	return m
}

func (m *flvMuxer) run() {
	defer m.wg.Done()

	// 这个函数里面就是创建了一个叫inner的新的go routine, 在这个routine里面处理请求, 在外面管理这个routine的生命周期
	err := func() error {
		var innerReady chan struct{} // 接收inner的ready信号
		var innerErr chan error      // 接收inner的错误报告
		var innerCtx context.Context // 保存inner的context
		var innerCtxCancel func()    // inner的context的cancel函数

		createInner := func() {
			innerReady = make(chan struct{})
			innerErr = make(chan error)
			innerCtx, innerCtxCancel = context.WithCancel(context.Background())
			go func() {
				innerErr <- m.runInner(innerCtx, innerReady)
			}()
		}

		createInner()

		isReady := false
		isRecreating := false
		recreateTimer := newEmptyTimer()

		for {
			select {
			case <-m.ctx.Done(): // muxer
				if !isRecreating {
					innerCtxCancel()
					<-innerErr
				}
				return errors.New("terminated")

			case req := <-m.chRequest: // 收到了请求, 则根据当前状态进行处理
				m.Log(logger.Info, "muxer received request")
				if req.ctx.IsAborted() {
					m.Log(logger.Error, "request context is already aborted")
				} else if req.ctx.Writer.Written() {
					m.Log(logger.Error, "response is already written")
				}
				switch {
				case isRecreating: // 如果正在重建, 则直接返回nil
					m.Log(logger.Info, "muxer is rebuilding")
					req.res <- nil

				case isReady: // 如果已经准备好, 则直接返回当前muxer
					m.Log(logger.Info, "muxer is ready")
					req.res <- m
					m.Log(logger.Info, "muxer returned res")

				default: // 如果还没有准备好, 则将请求放到队列里面
					m.Log(logger.Info, "inner not ready, put into request queue")
					m.requests = append(m.requests, req) // 设置一个请求超时自动过期返回错误的chan // 设置一个定时器, 5s内没反应就返回错误信息.
					go req.expireRequest()
				}

			case <-innerReady: // 收到了inner的ready信号, 则将状态设置为ready, 并且处理队列里面的请求
				isReady = true
				for _, req := range m.requests {
					req.res <- m
					req.reqHandled <- struct{}{}
				}
				m.requests = nil

			case err := <-innerErr: // 收到了inner的报错信息, 如果不是always remux, 则直接返回错误, 如果是always remux, 则重建inner
				innerCtxCancel()        // 删除子go routine
				if m.remoteAddr == "" { // created with "always remux"
					m.Log(logger.Info, "ERR: %v", err)
					m.clearQueuedRequests()
					isReady = false
					isRecreating = true
					recreateTimer = time.NewTimer(hlsMuxerRecreatePause)
				} else {
					return err
				}

			case <-recreateTimer.C:
				isRecreating = false
				createInner()
			}
		}
	}()

	m.ctxCancel()

	m.clearQueuedRequests()

	m.parent.muxerClose(m)

	m.Log(logger.Info, "destroyed (%v)", err)
}

func (m *flvMuxer) close() {
	m.ctxCancel()
}

func (m *flvMuxer) Log(level logger.Level, format string, args ...interface{}) {
	m.parent.Log(level, "[muxer %s] "+format, append([]interface{}{m.pathName}, args...)...)
}

func (m *flvMuxer) handleRequest(ctx *gin.Context) {
	if ctx.Writer.Written() {
		m.Log(logger.Error, "already written3")
	}
	writer, err := NewFlvWriter(ctx, m)
	if err != nil {
		m.Log(logger.Error, "failed to create flv writer")
		ctx.Abort()
		return
	}
	go func() {
		m.wg.Add(1)
		defer func() {
			m.wg.Done()
		}()
		err := writer.run()
		if err != nil {
			m.Log(logger.Error, err.Error())
		}
		ctx.Abort()
		m.packetBuffer.Delete(ctx)
	}()
}

func (m *flvMuxer) clearQueuedRequests() {
	for _, req := range m.requests {
		req.res <- nil
	}
	m.requests = nil
}

func (m *flvMuxer) runInner(innerCtx context.Context, ready chan struct{}) error {
	res := m.pathManager.readerAdd(pathReaderAddReq{
		author:   m,
		pathName: m.pathName,
		skipAuth: true,
	})
	if res.err != nil {
		return res.err
	}

	m.path = res.path

	defer m.path.readerRemove(pathReaderRemoveReq{author: m})

	go func() {
		err := m.pullStream(res.stream)
		if err != nil {
			m.Log(logger.Error, "pull from source failed")
		}
	}()

	ready <- struct{}{}

	closeCheckTicker := time.NewTicker(closeCheckPeriod * 2) // 每两秒检查一次是否有client拉流
	defer closeCheckTicker.Stop()

	for {
		select {
		case <-closeCheckTicker.C:
			if m.remoteAddr != "" { // 如果没有设置持续mux的话
				t := time.Unix(0, atomic.LoadInt64(m.lastRequestTime))
				if time.Since(t) >= closeAfterInactivity {
					// 关闭所有readers
					return fmt.Errorf("not used anymore")
				}
			}

		case <-innerCtx.Done():
			// TODO: 这里需要关闭flvWriters
			return fmt.Errorf("terminated")
		}
	}

}

// apiItem返回一个apiFlvMuxer, 用于api观测flv服务运行状态
func (m *flvMuxer) apiItem() *apiFlvMuxer {
	return &apiFlvMuxer{
		Path:        m.pathName,
		Created:     m.created,
		LastRequest: time.Unix(0, atomic.LoadInt64(m.lastRequestTime)),
		BytesSent:   atomic.LoadUint64(m.bytesSent),
	}
}

func (m *flvMuxer) apiReaderDescribe() pathAPISourceOrReader {
	return pathAPISourceOrReader{Type: "flvMuxer", ID: "flvMuxer:" + m.pathName}
}

func (m *flvMuxer) processRequest(req *flvMuxerHandleRequestReq) {
	select {
	case m.chRequest <- req:
		m.Log(logger.Info, "processing request from m.chRequest...")
	case <-m.ctx.Done():
		req.res <- nil
	}
}

// 用于持续拉去RTP packet到muxer的buffer中, 并封装为flv tag, 待请求
func (m *flvMuxer) pullStream(st *stream) error {
	//for med, _ := range st.smedias {
	//	m.Log(logger.Error, "med: "+string(med.Type))
	//	for _, fm := range med.Formats {
	//		m.Log(logger.Error, "\tformat: "+fm.Codec())
	//	}
	//}

	var videoFormatH264 *formats.H264
	videoMedia := st.medias().FindFormat(&videoFormatH264)
	//videoMedia.Formats
	//temp := make([]*rtp.Packet, 10)

	// 先暂时实现一个H264的方案, 之后需要添加H265, Opus等
	if videoFormatH264 != nil {
		st.readerAdd(m, videoMedia, videoFormatH264, func(unit formatprocessor.Unit) {
			pkts := unit.GetRTPPackets()
			//h264Unit := unit.(*formatprocessor.UnitH264)
			//h264Unit.AU
			//m.packetBuffer.Range(func(key, value any) bool { // 遍历map的每一个key, 往里面放flv包
			//	select { // 如果对应的chan有空闲的buffer, 将p放进去, 如果没有, 直接丢, 不要阻塞在这个地方
			//	case value.(chan *formatprocessor.Unit) <- &unit:
			//	default:
			//	}
			//	return true
			//})
			for _, pkt := range pkts { // 自己处理太麻烦了
				m.packetBuffer.Range(func(key, value any) bool { // 遍历map的每一个key, 往里面放flv包
					select { // 如果对应的chan有空闲的buffer, 将p放进去, 如果没有, 直接丢, 不要阻塞在这个地方
					case value.(chan *rtp.Packet) <- pkt:
					default:
					}
					return true
				})
			}
		})
	}

	var audioFormatMPEG4Audio *formats.MPEG4Audio
	audioMedia := st.medias().FindFormat(&audioFormatMPEG4Audio)

	if audioMedia != nil {
		//m.Log(logger.Error, "read audio")
		st.readerAdd(m, audioMedia, audioFormatMPEG4Audio, func(unit formatprocessor.Unit) {
			pkts := unit.GetRTPPackets()
			for _, pkt := range pkts {
				m.packetBuffer.Range(func(key, value any) bool {
					select {
					case value.(chan *rtp.Packet) <- pkt:
					default:
					}
					return true
				})
			}
		})
	}

	return nil
}

func (r *flvMuxerHandleRequestReq) expireRequest() {
	r.timer = time.NewTimer(time.Second * 5)
	select {
	case <-r.timer.C: // 请求超时, 直接返回404
		r.ctx.Writer.WriteHeader(404)
	case <-r.reqHandled:
		r.timer.Stop()
		return
	}
}
