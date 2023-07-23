package core

import (
	"errors"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/gwuhaolin/livego/utils/pio"
	"github.com/pion/rtp"
	"time"
)

var (
	flvHeader = []byte{0x46, 0x4c, 0x56, 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
)

type flvPacket struct {
	header []byte
	data   []byte
}

// FlvWriter 从buffer中持续写flv tag到远端
type FlvWriter struct {
	remoteAddr string
	streamName string
	ctx        *gin.Context

	timeout            time.Duration
	PreTime            time.Time
	buf                []byte
	BaseTimestamp      uint32
	LastVideoTimestamp uint32
	LastAudioTimestamp uint32
	parent             *flvMuxer
	closed             bool
	chClose            chan struct{}
}

func PutI24BE(b []byte, v int32) {
	b[0] = byte(v >> 16)
	b[1] = byte(v >> 8)
	b[2] = byte(v)
}

func PutU8(b []byte, v uint8) {
	b[0] = v
}

// 将相关数据转化成大端, 写进header里面
func (p *flvPacket) writeHeader(typeId int, dataLen int, timestampBase uint32, timestampExt uint32) {
	h := make([]byte, 11)
	PutU8(h[0:1], uint8(typeId))
	PutI24BE(h[1:4], int32(dataLen))
	PutI24BE(h[4:7], int32(timestampBase))
	PutU8(h[7:8], uint8(timestampExt))

	p.header = h
}

func packFlv(pkt *rtp.Packet) *flvPacket {
	p := &flvPacket{
		header: nil,
		data:   []byte{0x01, 0x22},
	}
	return p
}

func NewFlvWriter(ctx *gin.Context, parent *flvMuxer) (*FlvWriter, error) {
	// TODO: 填充某些字段
	if ctx.Writer.Written() {
		parent.Log(logger.Error, "already written1")
	}

	writer := &FlvWriter{
		remoteAddr: ctx.RemoteIP(),
		streamName: parent.pathName,
		buf:        make([]byte, 11),
		parent:     parent,
		ctx:        ctx,
	}

	if ctx.Writer.Written() {
		parent.Log(logger.Error, "already written!!!")
	}

	ctx.Header("Access-Control-Allow-Origin", "*")
	ctx.Header("Access-Control-Allow-Credentials", "true")
	//ctx.Header("Transfer-Encoding", "chunked")
	ctx.Header("Content-Type", "application/octet-stream")

	ctx.Writer.WriteHeader(200)

	if ctx.Writer.Written() {
		writer.Log(logger.Error, "already written")
	}
	_, err := ctx.Writer.Write(flvHeader)
	if err != nil {
		writer.Log(logger.Error, "error writing flv header: "+err.Error()+", content length: "+ctx.Writer.Header().Get("Content-Length"))
		return nil, err
	}
	pio.PutI32BE(writer.buf[:4], 0)
	_, err = ctx.Writer.Write(writer.buf[:4])
	if err != nil {
		writer.Log(logger.Error, "error writing blanks: "+err.Error())
		return nil, err
	}

	// TODO: 记得在Writer结束之前销毁里面的对应的kv
	chPacketFlvWriter := make(chan *flvPacket, 64)
	parent.packetBuffer.Store(ctx, chPacketFlvWriter)
	return writer, nil
}

func (w *FlvWriter) run() error {
	packetQueue, err := w.parent.packetBuffer.Load(w.ctx)
	if err != true {
		return errors.New("failed to find corresponding packet queue")
	}
	for {
		select {
		case pkg := <-packetQueue.(chan *flvPacket):
			w.Log(logger.Error, "flv data: "+string(pkg.data))
			writeLen, err := w.ctx.Writer.Write(pkg.data)
			if err != nil {
				w.Log(logger.Error, "error writing package")
				return err
			}
			w.Log(logger.Error, "write pkg succeed, write len: "+string(rune(writeLen)))
			w.ctx.Writer.Flush()
		}
	}
	return nil
}

func (w *FlvWriter) Log(level logger.Level, format string, args ...interface{}) {
	w.parent.Log(level, "[writer %s] "+format, append([]interface{}{w.remoteAddr}, args...)...)
}
