package core

import (
	"errors"
	"github.com/bluenviron/gortsplib/v3/pkg/formats/rtph264"
	"github.com/bluenviron/gortsplib/v3/pkg/formats/rtpmpeg4audio"
	"github.com/bluenviron/mediamtx/internal/logger"
	"github.com/gin-gonic/gin"
	"github.com/pion/rtp"
	"strconv"
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

type flvTagHeader struct {
	tagType   uint8
	timeStamp uint32
}

type flvTag struct {
	header  flvTagHeader
	payload []byte
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
	ctx.Header("Transfer-Encoding", "chunked")
	ctx.Header("Content-Type", "video/x-flv")

	ctx.Writer.WriteHeader(200)

	hdrLen := strconv.FormatInt(int64(len(flvHeader)), 16)
	_, err := ctx.Writer.Write([]byte(hdrLen + "\r\n"))
	if err != nil {
		return nil, err
	}
	_, err = ctx.Writer.Write(append(flvHeader, byte('\r'), byte('\n')))
	if err != nil {
		writer.Log(logger.Error, "error writing flv header: "+err.Error()+", content length: "+ctx.Writer.Header().Get("Content-Length"))
		return nil, err
	}

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

	for {
		pkt, _ := <-packetQueue.(chan *rtp.Packet)
		if pkt.PayloadType == 96 { // 收到的是H264的视频包
			//w.Log(logger.Error, "payload hdr: 0x%02X 0x%02X 0x%02X 0x%02X", pkt.Payload[0], pkt.Payload[1], pkt.Payload[2], pkt.Payload[3])
			if _, _, err := h264Decoder.Decode(pkt); err != nil {
				//if err == rtph264.ErrMorePacketsNeeded {
				//	continue
				//} else {
				//	return err
				//}
			} else {
				// TODO: 将nalus打包成一个flv tag并发送
				//for _, nalu := range nalus {
				//	//tagType := FLV_TAG_VIDEO
				//	//StreamID := []byte{0x00, 0x00, 0x00}
				//	//w.Log(logger.Info, "nalu hdr: 0x%02X", nalu[0]&0x1f)
				//}
			}
		} else if pkt.PayloadType == 97 { // 收到的是mpeg-4 audio的音频包

		}
		//if nalus, ts, err := h264Decoder.
		//unit, _ := <-packetQueue.(chan *formatprocessor.Unit)
		//w.Log(logger.Info, "getting unit...")
		//for _, nalu := range (*unit).(*formatprocessor.UnitH264).AU {
		//	w.Log(logger.Info, "nalu header byte: %02X", nalu[0])
		//}

		//if ok {
		//	w.Log(logger.Info, pkt.String())
		//	if startTimeStamp == 0 {
		//		startTimeStamp = pkt.Timestamp
		//	}
		//	if !pkt.Marker { // 不是视频帧的最后一包
		//		tempList = append(tempList, pkt) // 包放进临时列表中
		//	} else { // 视频帧的最后一包, 需要合成一帧
		//		ts := pkt.Timestamp - startTimeStamp // 生成时间戳
		//		var tagType uint8
		//		if pkt.PayloadType == 96 { // 生成tag类型标签
		//			tagType = FLV_TAG_VIDEO
		//		} else if pkt.PayloadType == 97 {
		//			tagType = FLV_TAG_AUDIO
		//		} else {
		//			tagType = FLV_TAG_SCRIPTDATA
		//		}
		//	}
		//	// TODO: 从rtp包中获生成flv tag所需的所有数据
		//	/*
		//		已获取的:
		//		1. timestamp
		//		2. tagType
		//		3. streamID
		//		4. filter
		//		5. reserved
		//		6. datasize(最后获取)
		//	*/
		//	preTagSize = 0
		//
		//	pio.PutI32BE(w.buf[:4], int32(preTagSize))
		//	_, err = w.ctx.Writer.Write(w.buf[:4]) // 写prevtagsize
		//	if err != nil {
		//		w.Log(logger.Error, "error writing prev tag size: "+err.Error())
		//		return err
		//	}
		//
		//}
	}
}

func (tg *flvTag) marshall() []byte {
	return nil
}

func (w *FlvWriter) Log(level logger.Level, format string, args ...interface{}) {
	w.parent.Log(level, "[writer %s] "+format, append([]interface{}{w.remoteAddr}, args...)...)
}
