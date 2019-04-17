package rsocket

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rsocket/rsocket-go/common"
	"github.com/rsocket/rsocket-go/common/logger"
	"github.com/rsocket/rsocket-go/payload"
	"github.com/rsocket/rsocket-go/rx"
)

const startupPenalty = float64(math.MaxInt64 >> 12)
const inactivityFactor = float64(500)
const defaultInitialInterArrivalTime = 1e6

type weightedSocket struct {
	mutex    *sync.Mutex
	origin   ClientSocket
	supplier *socketSupplier

	lowerQuantile, higherQuantile, median common.Quantile

	stamp, stamp0    int64
	interArrivalTime common.Ewma
	duration         int64 // instantaneous cumulative duration

	pendingStreams int32
	pending        int32
	availability   float64
}

func (p *weightedSocket) FireAndForget(msg payload.Payload) {
	atomic.AddInt32(&p.pendingStreams, 1)
	defer atomic.AddInt32(&p.pendingStreams, -1)
	p.origin.FireAndForget(msg)
}

func (p *weightedSocket) MetadataPush(msg payload.Payload) {
	atomic.AddInt32(&p.pendingStreams, 1)
	defer atomic.AddInt32(&p.pendingStreams, -1)
	p.origin.MetadataPush(msg)
}

func (p *weightedSocket) RequestResponse(msg payload.Payload) rx.Mono {
	start := p.latency0()
	return p.origin.RequestResponse(msg).
		DoFinally(func(ctx context.Context, st rx.SignalType) {
			p.latency2(start)
		})
}

func (p *weightedSocket) RequestStream(msg payload.Payload) rx.Flux {
	atomic.AddInt32(&p.pendingStreams, 1)
	return p.origin.RequestStream(msg).DoFinally(func(ctx context.Context, st rx.SignalType) {
		atomic.AddInt32(&p.pendingStreams, -1)
		if st == rx.SignalError {
			// TODO: handle error
		}
	})
}

func (p *weightedSocket) RequestChannel(msgs rx.Publisher) rx.Flux {
	atomic.AddInt32(&p.pendingStreams, 1)
	return p.origin.RequestChannel(msgs).DoFinally(func(ctx context.Context, st rx.SignalType) {
		atomic.AddInt32(&p.pendingStreams, -1)
		if st == rx.SignalError {
			// TODO: handle error
		}
	})
}

func (p *weightedSocket) String() string {
	return fmt.Sprintf("Socket{%s}", p.supplier.u)
}

func (p *weightedSocket) Close() error {
	return p.origin.Close()
}

func (p *weightedSocket) latency0() int64 {
	now := common.NowInMicrosecond()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.interArrivalTime.Insert(float64(now - p.stamp))
	if v := now - p.stamp0; v > 0 {
		p.duration += v * int64(p.pending)
	}
	p.pending++
	p.stamp = now
	p.stamp0 = now
	return now
}

func (p *weightedSocket) latency1(start int64) int64 {
	now := common.NowInMicrosecond()
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if v := now - p.stamp0; v > 0 {
		p.duration += v * int64(p.pending)
	}
	p.duration -= now - start
	p.pending--
	p.stamp0 = now
	return now
}

func (p *weightedSocket) latency2(start int64) {
	end := p.latency1(start)
	rtt := float64(end - start)
	p.mutex.Lock()
	defer p.mutex.Unlock()
	logger.Infof("RTT: socket=%s, rtt=%f\n", p, rtt)
	p.median.Insert(rtt)
	p.lowerQuantile.Insert(rtt)
	p.higherQuantile.Insert(rtt)
}

func (p *weightedSocket) getPredictedLatency() float64 {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	now := common.NowInMicrosecond()
	var elapsed int64 = 1
	if v := now - p.stamp; v > elapsed {
		elapsed = v
	}
	var weight, prediction float64 = 0, p.median.Estimation()
	if prediction == 0 {
		if p.pending != 0 {
			weight = startupPenalty + float64(p.pending)
		}
	} else if p.pending == 0 && elapsed > int64(inactivityFactor*p.interArrivalTime.Value()) {
		p.median.Insert(0)
		weight = p.median.Estimation()
	} else {
		predicted := prediction * float64(p.pending)
		instant := p.instantaneous(now)
		if predicted < float64(instant) {
			weight = float64(instant) / float64(p.pending)
		} else {
			weight = prediction
		}
	}
	logger.Infof("weight: %f\n", weight)
	return weight
}

func (p *weightedSocket) instantaneous(now int64) float64 {
	return float64(p.duration + (now-p.stamp0)*int64(p.pending))
}

func newWeightedSocket(lowerQuantile, higherQuantile common.Quantile, origin ClientSocket, supplier *socketSupplier) *weightedSocket {
	now := common.NowInMicrosecond()
	return &weightedSocket{
		mutex:            &sync.Mutex{},
		origin:           origin,
		supplier:         supplier,
		availability:     1,
		lowerQuantile:    lowerQuantile,
		higherQuantile:   higherQuantile,
		median:           common.NewMedianQuantile(),
		interArrivalTime: common.NewEwma(1, time.Minute, defaultInitialInterArrivalTime),
		stamp:            now,
		stamp0:           now,
	}
}
