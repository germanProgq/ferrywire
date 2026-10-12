package transport

import (
	"sync"
	"sync/atomic"
	"time"
)

type TransportConfig struct {
	MaxReconnectAttempts int
	MaxQueueSize         int
	KeepAliveInterval    time.Duration
}

type Transport interface {
	Start() error
	Send(data []byte) error
	Receive(callback func([]byte))
}

func DefaultConfig() TransportConfig {
	return TransportConfig{
		MaxReconnectAttempts: 999999,
		MaxQueueSize:         8192,
		KeepAliveInterval:    10 * time.Second,
	}
}

type BaseTransport struct {
	config    TransportConfig
	running   atomic.Int32
	connected atomic.Int32

	receiveCallback func([]byte)
	mu              sync.RWMutex
}

func NewBaseTransport(config TransportConfig) *BaseTransport {
	return &BaseTransport{config: config}
}

func (b *BaseTransport) Start() error {
	b.running.Store(1)
	return nil
}

func (b *BaseTransport) IsRunning() bool {
	return b.running.Load() == 1
}

func (b *BaseTransport) IsConnected() bool {
	return b.connected.Load() == 1
}

func (b *BaseTransport) SetConnected(connected bool) {
	if connected {
		b.connected.Store(1)
	} else {
		b.connected.Store(0)
	}
}

func (b *BaseTransport) Receive(callback func([]byte)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.receiveCallback = callback
}

func (b *BaseTransport) CallReceive(data []byte) {
	b.mu.RLock()
	cb := b.receiveCallback
	b.mu.RUnlock()
	if cb != nil {
		cb(data)
	}
}

func (b *BaseTransport) GetConfig() TransportConfig {
	return b.config
}
