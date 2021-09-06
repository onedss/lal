// Copyright 2021, Chef.  All rights reserved.
// https://github.com/onedss/lal
//
// Use of this source code is governed by a MIT-style license
// that can be found in the License file.
//
// Author: Chef (191201771@qq.com)

package hls

import (
	"testing"

	"github.com/onedss/lal/pkg/base"
	"github.com/onedss/lal/pkg/mpegts"
	"github.com/onedss/naza/pkg/assert"
)

var (
	fh    []byte
	poped []base.RtmpMsg
)

type qo struct {
}

func (q *qo) OnPatPmt(b []byte) {
	fh = b
}

func (q *qo) OnPop(msg base.RtmpMsg) {
	poped = append(poped, msg)
}

func TestQueue(t *testing.T) {
	goldenRtmpMsg := []base.RtmpMsg{
		{
			Header: base.RtmpHeader{
				MsgTypeId: base.RtmpTypeIdAudio,
			},
			Payload: []byte{0xAF},
		},
		{
			Header: base.RtmpHeader{
				MsgTypeId: base.RtmpTypeIdVideo,
			},
			Payload: []byte{0x17},
		},
	}

	q := &qo{}
	queue := NewQueue(8, q)
	for i := range goldenRtmpMsg {
		queue.Push(goldenRtmpMsg[i])
	}
	assert.Equal(t, mpegts.FixedFragmentHeader, fh)
	assert.Equal(t, goldenRtmpMsg, poped)
}
