package oneme

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/germanProgq/ferrywire/transport"
)

type Transport struct {
	b     *transport.Base
	token string
	uid   int64
	exit  bool

	client *Client
	ch     *CallHandler
}

func NewTransport(isExit bool, maxToken string, maxUid int64, config transport.Config) *Transport {
	return &Transport{
		b:     transport.NewBase(config),
		token: maxToken,
		uid:   maxUid,
		exit:  isExit,
	}
}

func (t *Transport) Receive(callback func([]byte)) {
	t.b.Receive(callback)
}

func (t *Transport) Fatal() <-chan error {
	return t.b.Fatal()
}

func (t *Transport) Start() error {
	if err := t.b.Start(); err != nil {
		return err
	}

	t.client = NewClient()
	if err := t.client.Connect(); err != nil {
		return fmt.Errorf("connect to MAX: %w", err)
	}
	if err := t.client.LoginByToken(t.token); err != nil {
		t.client.Close()
		return fmt.Errorf("login to MAX: %w", err)
	}
	go t.watchClient()

	if t.exit {
		t.ch = startIncomingListener(t.client, t.b.CallReceive, t.b.ReportFatal)
	} else {
		t.ch = startOutgoingCall(t.client, t.uid, t.b.CallReceive, t.b.ReportFatal)
	}
	slog.Debug("MAX transport started", "exit", t.exit)
	return nil
}

func (t *Transport) watchClient() {
	<-t.client.Done()
	t.b.ReportFatal(errors.New("MAX websocket connection closed"))
}

func (t *Transport) Stop() {
	t.b.Stop()
	if t.ch != nil {
		t.ch.close()
	}
	if t.client != nil {
		t.client.Close()
	}
}

func (t *Transport) Send(data []byte) error {
	t.ch.Send(data)
	return nil
}
