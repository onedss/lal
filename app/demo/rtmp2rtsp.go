package main

import (
	"flag"
	"fmt"
	"github.com/onedss/lal/pkg/base"
	"github.com/onedss/lal/pkg/httpflv"
	"github.com/onedss/lal/pkg/remux"
	"github.com/onedss/lal/pkg/rtmp"
	"github.com/onedss/lal/pkg/rtprtcp"
	"github.com/onedss/lal/pkg/rtsp"
	"github.com/onedss/lal/pkg/sdp"
	"github.com/onedss/naza/pkg/nazalog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var aliveSessionCount int32

func main() {
	_ = nazalog.Init(func(option *nazalog.Option) {
		option.AssertBehavior = nazalog.AssertFatal
		option.ShortFileFlag = false
	})
	defer nazalog.Sync()

	inRtmpUrl, outRtspUrl, filename := parseFlag()

	nazalog.Infof("Parse flag succ. inRtmpUrl=%s, outRtspUrl=%s", inRtmpUrl, outRtspUrl)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		atomic.AddInt32(&aliveSessionCount, 1)
		pull(inRtmpUrl, outRtspUrl, filename)
		wg.Done()
		atomic.AddInt32(&aliveSessionCount, -1)
	}()
	wg.Wait()

	time.Sleep(1 * time.Second)

	nazalog.Info("< main End.%d", atomic.LoadInt32(&aliveSessionCount))
}

func pull(rtmpUrl string, rtspUrl string, filename string) {
	var (
		w   httpflv.FlvFileWriter
		err error
	)

	if filename != "" {
		err = w.Open(filename)
		nazalog.Assert(nil, err)
		defer w.Dispose()
	}

	pullSession := rtmp.NewPullSession(func(option *rtmp.PullSessionOption) {
		option.PullTimeoutMs = 30000
		option.ReadAvTimeoutMs = 30000
	})
	pushSession := rtsp.NewPushSession(func(option *rtsp.PushSessionOption) {
		option.OverTcp = true
	})

	remuxer := remux.NewRtmp2RtspRemuxer(
		func(sdpCtx sdp.LogicContext) {
			// remuxer完成前期工作，生成sdp并开始push
			nazalog.Info("start push.")
			err := pushSession.Push(rtspUrl, sdpCtx)
			nazalog.Assert(nil, err)
			nazalog.Info("push succ.")

		},
		func(pkt rtprtcp.RtpPacket) {
			_ = pushSession.WriteRtpPacket(pkt) // remuxer的数据给push发送
		},
	)

	nazalog.Info("start pull.")
	err = pullSession.Pull(rtmpUrl, remuxer.OnRtmpMsg) // pull接收的数据放入remuxer中
	if err != nil {
		nazalog.Errorf("pull failed. err=%+v", err)
		return
	}
	nazalog.Assert(nil, err)
	nazalog.Info("pull succ.")

	err = <-pullSession.WaitChan()
	nazalog.Debugf("< session.WaitChan. [%s] err=%+v", pullSession.UniqueKey(), err)
}

func parseFlag() (inRtmpUrl string, outRtspUrl string, filename string) {
	i := flag.String("i", "", "specify pull rtmp url")
	o := flag.String("o", "", "specify push rtsp url")
	f := flag.String("f", "", "specify ouput file")
	flag.Parse()
	if *i == "" || *o == "" {
		flag.Usage()
		_, _ = fmt.Fprintf(os.Stderr, `Example:
  %s -i rtmp://localhost:1935/live/test110 -o rtsp://localhost:544/test220.sdp -f out.mp3
`, os.Args[0])
		base.OsExitAndWaitPressIfWindows(1)
	}
	return *i, *o, *f
}
