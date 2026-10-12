package transport

import (
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	MaxReconnectAttempts int
	MaxQueueSize         int
	KeepAliveInterval    time.Duration
}

type Transport interface {
	Start() error
	Stop()
	Send(data []byte) error
	Receive(callback func([]byte))
	Fatal() <-chan error
}

func DefaultConfig() Config {
	return Config{
		MaxReconnectAttempts: 999999,
		MaxQueueSize:         8192,
		KeepAliveInterval:    10 * time.Second,
	}
}

type Base struct {
	config    Config
	running   atomic.Int32
	connected atomic.Int32

	receiveCallback func([]byte)
	mu              sync.RWMutex
	fatal           chan error
}

func NewBase(config Config) *Base {
	return &Base{config: config, fatal: make(chan error, 1)}
}

func (b *Base) Start() error {
	b.running.Store(1)
	return nil
}

func (b *Base) Stop() {
	b.running.Store(0)
}

func (b *Base) Fatal() <-chan error {
	return b.fatal
}

func (b *Base) ReportFatal(err error) {
	if !b.IsRunning() {
		return
	}
	select {
	case b.fatal <- err:
	default:
	}
}

func (b *Base) IsRunning() bool {
	return b.running.Load() == 1
}

func (b *Base) IsConnected() bool {
	return b.connected.Load() == 1
}

func (b *Base) SetConnected(connected bool) {
	if connected {
		b.connected.Store(1)
	} else {
		b.connected.Store(0)
	}
}

func (b *Base) Receive(callback func([]byte)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.receiveCallback = callback
}

func (b *Base) CallReceive(data []byte) {
	b.mu.RLock()
	cb := b.receiveCallback
	b.mu.RUnlock()
	if cb != nil {
		cb(data)
	}
}

func (b *Base) GetConfig() Config {
	return b.config
}
