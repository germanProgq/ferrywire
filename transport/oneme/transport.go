package oneme

import (
	"log/slog"
	"github.com/germanProgq/ferrywire/transport"
)

type Transport struct {
	b     *transport.Base
	token string
	uid   int64
	exit  bool

	oneMeClient Client
	ch          *CallHandler
}

func (t *Transport) Receive(callback func([]byte)) {
	t.b.Receive(callback)
}

func NewTransport(isExit bool, maxToken string, maxUid int64, config transport.Config) *Transport {
	return &Transport{
		b:     transport.NewBase(config),
		token: maxToken,
		uid:   maxUid,
		exit:  isExit,
	}
}

func (t *Transport) Start() error {
	slog.Debug("creating max client ...")
	t.oneMeClient = *NewClient()
	t.oneMeClient.Connect()
	t.oneMeClient.LoginByToken(t.token)

	if t.exit {
		slog.Debug("configured ch for exit node")
		t.ch = startIncomingListener(&t.oneMeClient)
	} else {
		slog.Debug("configured ch for client mode")
		t.ch = startOutgoingCall(&t.oneMeClient, t.uid)
	}

	slog.Debug("configured dc inbound")
	t.ch.dcInbound = func(data []byte) {
		t.b.CallReceive(data)
	}

	return t.b.Start()
}

func (t *Transport) Send(data []byte) error {
	t.ch.Send(data)
	return nil
}
