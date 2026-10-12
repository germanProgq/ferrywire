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
	Send(data []byte) error
	Receive(callback func([]byte))
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
}

func NewBase(config Config) *Base {
	return &Base{config: config}
}

func (b *Base) Start() error {
	b.running.Store(1)
	return nil
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
