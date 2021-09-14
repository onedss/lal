// Copyright 2021, Chef.  All rights reserved.
// https://github.com/onedss/lal
//
// Use of this source code is governed by a MIT-style license
// that can be found in the License file.
//
// Author: Chef (191201771@qq.com)

package remux

import (
	"math/rand"
	"time"

	"github.com/onedss/lal/pkg/aac"
	"github.com/onedss/lal/pkg/avc"
	"github.com/onedss/lal/pkg/base"
	"github.com/onedss/lal/pkg/hevc"
	"github.com/onedss/lal/pkg/rtprtcp"
	"github.com/onedss/lal/pkg/sdp"
	"github.com/onedss/naza/pkg/nazalog"
)

// TODO(chef): refactor 将analyze部分独立出来作为一个filter

var (
	// config
	// TODO(chef): 提供option，另外还有ssrc和pt都支持自定义
	maxAnalyzeAvMsgSize = 16
)

// 提供rtmp数据向sdp+rtp数据的转换
type Rtmp2RtspRemuxer struct {
	onSdp       OnSdp
	onRtpPacket OnRtpPacket

	analyzeDone        bool
	msgCache           []base.RtmpMsg
	vps, sps, pps, asc []byte
	audioPt            base.AvPacketPt
	videoPt            base.AvPacketPt

	audioSsrc   uint32
	videoSsrc   uint32
	audioPacker *rtprtcp.RtpPacker
	videoPacker *rtprtcp.RtpPacker

	seq uint16

	control rtprtcp.RtpControl
}

type OnSdp func(sdpCtx sdp.LogicContext)
type OnRtpPacket func(pkt rtprtcp.RtpPacket)

// @param onSdp:       每次回调为独立的内存块，回调结束后，内部不再使用该内存块
// @param onRtpPacket: 每次回调为独立的内存块，回调结束后，内部不再使用该内存块
//
func NewRtmp2RtspRemuxer(onSdp OnSdp, onRtpPacket OnRtpPacket) *Rtmp2RtspRemuxer {
	return &Rtmp2RtspRemuxer{
		onSdp:       onSdp,
		onRtpPacket: onRtpPacket,
		audioPt:     base.AvPacketPtUnknown,
		videoPt:     base.AvPacketPtUnknown,
		audioSsrc:   rand.Uint32(),
		videoSsrc:   rand.Uint32(),
	}
}

func (r *Rtmp2RtspRemuxer) OnRtmpMsg(msg base.RtmpMsg) {
	//var err error

	if msg.Header.MsgTypeId == base.RtmpTypeIdMetadata {
		// noop
		return
	}

	if !r.analyzeDone {
		if msg.Header.MsgTypeId == base.RtmpTypeIdAudio {
			controlByte := msg.Payload[0]
			r.control = parseRtmpControl(controlByte)
			r.audioPt = (base.AvPacketPt)(r.control.PacketType)
		}
		// 回调sdp
		ctx, err := sdp.NewPack(r.control.PacketType)
		nazalog.Assert(nil, err)
		r.analyzeDone = true
		r.onSdp(ctx)
	}
	// 正常阶段

	// 音视频头已通过sdp回调，rtp数据中不再包含音视频头
	//if msg.IsAvcKeySeqHeader() || msg.IsHevcKeySeqHeader() || msg.IsAacSeqHeader() {
	//	return
	//}

	//if msg.Header.MsgTypeId == base.RtmpTypeIdAudio || msg.Header.MsgTypeId == base.RtmpTypeIdVideo {
	//	nazalog.Println("BCD: ", hex.EncodeToString(msg.Payload[1:]))
	//}

	r.doRemux(msg)
}

func parseRtmpControl(control byte) rtprtcp.RtpControl {
	format := control >> 4 & 0xF
	sampleRate := control >> 2 & 0x3
	sampleSize := control >> 1 & 0x1
	channelNum := control & 0x1
	rtmpBodyControl := rtprtcp.MakeDefaultRtpControl()
	rtmpBodyControl.Format = format
	switch format {
	case 2:
		rtmpBodyControl.PacketType = uint8(base.RtpPacketTypeMpa)
	case 10:
		rtmpBodyControl.PacketType = uint8(base.RtpPacketTypeAac)
	default:
		rtmpBodyControl.PacketType = uint8(base.RtpPacketTypeMpa)
	}
	if sampleRate == 3 {
		rtmpBodyControl.SampleRate = 44.1
	}
	if sampleSize == 1 {
		rtmpBodyControl.SampleSize = 16
	}
	if channelNum == 1 {
		rtmpBodyControl.ChannelNum = 2
	}
	return rtmpBodyControl
}

func (r *Rtmp2RtspRemuxer) doRemux(msg base.RtmpMsg) {
	var rtppkts []rtprtcp.RtpPacket
	switch msg.Header.MsgTypeId {
	case base.RtmpTypeIdAudio:
		pkg := base.AvPacket{
			Timestamp:   msg.Header.TimestampAbs,
			PayloadType: r.audioPt,
			Payload:     msg.Payload[1:],
		}

		payload := make([]byte, 4+len(pkg.Payload))
		copy(payload[4:], pkg.Payload)
		//timeUnix:=time.Now().UnixNano() / 1e6
		//nazalog.Println(timeUnix)
		h := rtprtcp.MakeDefaultRtpHeader()
		h.Mark = 0
		packetType := r.control.PacketType
		h.PacketType = packetType
		h.Seq = r.genSeq()
		sampleRate := r.control.SampleRate
		channelNum := r.control.ChannelNum
		h.Timestamp = uint32(float64(pkg.Timestamp) * sampleRate * float64(channelNum))
		h.Ssrc = r.audioSsrc
		pkt := rtprtcp.MakeRtpPacket(h, payload)
		rtppkts = append(rtppkts, pkt)
	case base.RtmpTypeIdVideo:

	}

	for i := range rtppkts {
		r.onRtpPacket(rtppkts[i])
	}
}

func (r *Rtmp2RtspRemuxer) genSeq() (ret uint16) {
	r.seq++
	ret = r.seq
	return
}

// @param msg: 函数调用结束后，内部不持有`msg`内存块
//
func (r *Rtmp2RtspRemuxer) FeedRtmpMsg(msg base.RtmpMsg) {
	var err error

	if msg.Header.MsgTypeId == base.RtmpTypeIdMetadata {
		// noop
		return
	}

	// 我们需要先接收一部分rtmp数据，得到音频头、视频头
	// 并且考虑，流中只有音频或只有视频的情况
	// 我们把前面这个阶段叫做Analyze分析阶段

	if !r.analyzeDone {
		if msg.IsAvcKeySeqHeader() || msg.IsHevcKeySeqHeader() {
			if msg.IsAvcKeySeqHeader() {
				r.sps, r.pps, err = avc.ParseSpsPpsFromSeqHeader(msg.Payload)
				nazalog.Assert(nil, err)
			} else if msg.IsHevcKeySeqHeader() {
				r.vps, r.sps, r.pps, err = hevc.ParseVpsSpsPpsFromSeqHeader(msg.Payload)
				nazalog.Assert(nil, err)
			}
			r.doAnalyze()
			return
		}

		if msg.IsAacSeqHeader() {
			r.asc = msg.Clone().Payload[2:]
			r.doAnalyze()
			return
		}

		r.msgCache = append(r.msgCache, msg.Clone())
		r.doAnalyze()
		return
	}

	// 正常阶段

	// 音视频头已通过sdp回调，rtp数据中不再包含音视频头
	if msg.IsAvcKeySeqHeader() || msg.IsHevcKeySeqHeader() || msg.IsAacSeqHeader() {
		return
	}

	r.remux(msg)
}

func (r *Rtmp2RtspRemuxer) doAnalyze() {
	nazalog.Assert(false, r.analyzeDone)

	if r.isAnalyzeEnough() {
		if r.sps != nil && r.pps != nil {
			if r.vps != nil {
				r.videoPt = base.AvPacketPtHevc
			} else {
				r.videoPt = base.AvPacketPtAvc
			}
		}
		if r.asc != nil {
			r.audioPt = base.AvPacketPtAac
		}

		// 回调sdp
		ctx, err := sdp.Pack(r.vps, r.sps, r.pps, r.asc)
		nazalog.Assert(nil, err)
		r.onSdp(ctx)

		// 分析阶段缓存的数据
		for i := range r.msgCache {
			r.remux(r.msgCache[i])
		}
		r.msgCache = nil

		r.analyzeDone = true
	}
}

// 是否应该退出Analyze阶段
func (r *Rtmp2RtspRemuxer) isAnalyzeEnough() bool {
	// 音视频头都收集好了
	if r.sps != nil && r.pps != nil && r.asc != nil {
		return true
	}

	// 达到分析包数阈值了
	if len(r.msgCache) >= maxAnalyzeAvMsgSize {
		return true
	}

	return false
}

func (r *Rtmp2RtspRemuxer) remux(msg base.RtmpMsg) {
	var rtppkts []rtprtcp.RtpPacket
	switch msg.Header.MsgTypeId {
	case base.RtmpTypeIdAudio:
		rtppkts = r.getAudioPacker().Pack(base.AvPacket{
			Timestamp:   msg.Header.TimestampAbs,
			PayloadType: r.audioPt,
			Payload:     msg.Payload[2:],
		})
	case base.RtmpTypeIdVideo:
		rtppkts = r.getVideoPacker().Pack(base.AvPacket{
			Timestamp:   msg.Header.TimestampAbs,
			PayloadType: r.videoPt,
			Payload:     msg.Payload[5:],
		})
	}

	for i := range rtppkts {
		r.onRtpPacket(rtppkts[i])
	}
}

func (r *Rtmp2RtspRemuxer) getAudioPacker() *rtprtcp.RtpPacker {
	if r.audioPacker == nil {
		// TODO(chef): ssrc随机产生，并且整个lal没有在setup信令中传递ssrc
		r.audioSsrc = rand.Uint32()

		// TODO(chef): 如果rtmp不是以音视频头开始，也可能收到了帧数据，但是头不存在，目前该remux没有做过多容错判断，后续要加上，或者在输入层保证
		ascCtx, err := aac.NewAscContext(r.asc)
		if err != nil {
			nazalog.Errorf("parse asc failed. err=%+v", err)
		}
		clockRate, err := ascCtx.GetSamplingFrequency()
		if err != nil {
			nazalog.Errorf("get sampling frequency failed. err=%+v", err)
		}

		pp := rtprtcp.NewRtpPackerPayloadAac()
		r.audioPacker = rtprtcp.NewRtpPacker(pp, clockRate, r.audioSsrc)
	}
	return r.audioPacker
}

func (r *Rtmp2RtspRemuxer) getVideoPacker() *rtprtcp.RtpPacker {
	if r.videoPacker == nil {
		r.videoSsrc = rand.Uint32()
		pp := rtprtcp.NewRtpPackerPayloadAvcHevc(r.videoPt, func(option *rtprtcp.RtpPackerPayloadAvcHevcOption) {
			option.Typ = rtprtcp.RtpPackerPayloadAvcHevcTypeAvcc
		})
		r.videoPacker = rtprtcp.NewRtpPacker(pp, 90000, r.videoSsrc)
	}
	return r.videoPacker
}

func init() {
	rand.Seed(time.Now().UnixNano())
}
