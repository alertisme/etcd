package etcd

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
)

const (
	maxInflight = 4
)

const (
	participantPeer = iota
	idlePeer
	stoppedPeer
)

type peer struct {
	url      string
	queue    chan []byte
	status   int
	inflight atomicInt
	c        *http.Client
	mu       sync.RWMutex
	wg       sync.WaitGroup
}

func newPeer(url string, c *http.Client) *peer {
	return &peer{
		url:    url,
		status: idlePeer,
		c:      c,
	}
}

func (p *peer) participate() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = make(chan []byte)
	p.status = participantPeer
	for i := 0; i < maxInflight; i++ {
		p.wg.Add(1)
		go p.handle(p.queue)
	}
}

func (p *peer) idle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status == participantPeer {
		close(p.queue)
	}
	p.status = idlePeer
}

func (p *peer) stop() {
	p.mu.Lock()
	if p.status == participantPeer {
		close(p.queue)
	}
	p.status = stoppedPeer
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *peer) handle(queue chan []byte) {
	defer p.wg.Done()
	for d := range queue {
		p.post(d)
	}
}

func (p *peer) send(d []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch p.status {
	case participantPeer:
		select {
		case p.queue <- d:
		default:
			return fmt.Errorf("reach max serving")
		}
	case idlePeer:
		if p.inflight.Get() > maxInflight {
			return fmt.Errorf("reach max idle")
		}
		go func() {
			p.wg.Add(1)
			p.post(d)
			p.wg.Done()
		}()
	case stoppedPeer:
		return fmt.Errorf("sender stopped")
	}
	return nil
}

func (p *peer) post(d []byte) {
	p.inflight.Add(1)
	defer p.inflight.Add(-1)
	buf := bytes.NewBuffer(d)
	resp, err := p.c.Post(p.url, "application/octet-stream", buf)
	if err != nil {
		log.Println("post:", err)
		return
	}
	resp.Body.Close()
}

// An AtomicInt is an int64 to be accessed atomically.
type atomicInt int64

func (i *atomicInt) Add(d int64) {
	atomic.AddInt64((*int64)(i), d)
}

func (i *atomicInt) Get() int64 {
	return atomic.LoadInt64((*int64)(i))
}

func (i *atomicInt) Set(n int64) {
	atomic.StoreInt64((*int64)(i), n)
}
