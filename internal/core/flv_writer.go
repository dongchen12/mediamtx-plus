package core

import (
	"encoding/binary"
	"errors"
	"github.com/bluenviron/gortsplib/v3/pkg/formats/rtph264"
	"github.com/bluenviron/gortsplib/v3/pkg/formats/rtpmpeg4audio"
	"github.com/bluenviron/mediacommon/pkg/codecs/h264"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/gwuhaolin/livego/utils/pio"
	"github.com/pion/rtp"
	"time"
)

var (
	flvHeader = []byte{0x46, 0x4c, 0x56, 0x01, 0x05, 0x00, 0x00, 0x00, 0x09}
)

const (
	FLV_TAG_AUDIO      = byte(8)
	FLV_TAG_VIDEO      = byte(9)
	FLV_TAG_SCRIPTDATA = byte(12)
)

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

// 将相关数据转化成大端, 写进header里面
//func (p *flvPacket) writeHeader(typeId int, dataLen int, timestampBase uint32, timestampExt uint32) {
//	h := make([]byte, 11)
//	PutU8(h[0:1], uint8(typeId))
//	PutI24BE(h[1:4], int32(dataLen))
//	PutI24BE(h[4:7], int32(timestampBase))
//	PutU8(h[7:8], uint8(timestampExt))
//
//	p.header = h
//}

func NewFlvWriter(ctx *gin.Context, parent *flvMuxer) (*FlvWriter, error) {
	//if ctx.Writer.Written() {
	//	parent.Log(logger.Error, "already written1")
	//}

	writer := &FlvWriter{
		remoteAddr: ctx.RemoteIP(),
		streamName: parent.pathName,
		buf:        make([]byte, 11),
		parent:     parent,
		ctx:        ctx,
	}

	//if ctx.Writer.Written() {
	//	parent.Log(logger.Error, "already written!!!")
	//}

	ctx.Header("Access-Control-Allow-Origin", "*")
	ctx.Header("Access-Control-Allow-Credentials", "true")
	ctx.Header("Content-Type", "application/octet-stream")
	ctx.Header("Content-Length", "-1")
	ctx.Writer.WriteHeader(200)

	ctx.Writer.Write(flvHeader)
	ctx.Writer.Write([]byte{0, 0, 0, 0})

	chPacketFlvWriter := make(chan *rtp.Packet, 256)
	parent.packetBuffer.Store(ctx, chPacketFlvWriter)
	return writer, nil
}

func (w *FlvWriter) run() (err error) {
	//defer func() {
	//	if r := recover(); r != nil {
	//		err = fmt.Errorf("failed to run flv writer\n")
	//	}
	//}()
	packetQueue, isErr := w.parent.packetBuffer.Load(w.ctx)
	if isErr != true {
		return errors.New("failed to find corresponding packet queue")
	}
	//H264Depacketizer := codecs.H264Packet{}
	//tempList := make([]*rtp.Packet, 10) // 存放同一个时间戳的rtp包
	//var startTimeStamp uint32 = 0       // 存放当前列表中的包的时间戳
	//var preTagSize uint32 = 0
	h264Decoder := rtph264.Decoder{}
	err = h264Decoder.Init()
	if err != nil {
		return err
	}
	mpeg4audioDecoder := rtpmpeg4audio.Decoder{}
	err = mpeg4audioDecoder.Init()
	if err != nil {
		return err
	}

	//writer := w.ctx.Writer
	//var start = false
	var startVideoTimeStamp uint32 = 0
	//va
	//r startAudioTimeStamp uint32 = 0
	var prevTagSize = 0

	for {
		pkt, _ := <-packetQueue.(chan *rtp.Packet)

		if pkt.PayloadType == 96 { // 收到的是H264的视频包
			switch h264.NALUType(pkt.Payload[0] & 0x1F) {
			case h264.NALUTypeSPS:
				w.Log(logger.Error, "SPS")
			case h264.NALUTypePPS:
				w.Log(logger.Error, "PPS")
			}
			if nalus, _, err := h264Decoder.DecodeUntilMarker(pkt); err != nil {
				if err == rtph264.ErrMorePacketsNeeded {
					continue
				} else {
					return err
				}
			} else {
				// TODO: 将nalus打包成一个flv tag并发送
				dataBytes, err := h264.AVCCMarshal(nalus)
				if err != nil {
					return err
				}
				dataLen := len(dataBytes)
				if startVideoTimeStamp == 0 {
					startVideoTimeStamp = pkt.Timestamp
				}
				h := make([]byte, 11)
				timestamp := pkt.Timestamp - startVideoTimeStamp
				tagType := byte(0x09)
				streamID := uint32(0)
				timeStampbase := timestamp & 0xffffff
				timestampExt := timestamp >> 24 & 0xff

				pio.PutU8(h[0:1], tagType)
				pio.PutI24BE(h[1:4], int32(dataLen))
				pio.PutI24BE(h[4:7], int32(timeStampbase))
				pio.PutU8(h[7:8], uint8(timestampExt))
				pio.PutI24BE(h[8:11], int32(streamID))

				tsbuf := make([]byte, 4)
				binary.BigEndian.PutUint32(tsbuf, uint32(prevTagSize))
				prevTagSize = dataLen + 11
			}
		} else if pkt.PayloadType == 97 { // 收到的是mpeg-4 audio的音频包

		}
	}
}

func (w *FlvWriter) Log(level logger.Level, format string, args ...interface{}) {
	w.parent.Log(level, "[writer %s] "+format, append([]interface{}{w.remoteAddr}, args...)...)
}
