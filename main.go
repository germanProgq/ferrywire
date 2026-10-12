package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	_ "github.com/wlynxg/anet"

	"github.com/germanProgq/ferrywire/socks5"
	"github.com/germanProgq/ferrywire/transport"
	"github.com/germanProgq/ferrywire/transport/oneme"
	"github.com/germanProgq/ferrywire/transport/yandex"
	"github.com/germanProgq/ferrywire/tunnel"
)

func main() {
	exitNode := flag.Bool("exit-node", false, "Run as exit node (needs root)")
	client := flag.Bool("client", false, "Run as client")
	debug := flag.Bool("debug", false, "Enable verbose debug logging")
	socksAddr := flag.String("socks5", "127.0.0.1:1080", "SOCKS5 listen address")
	transportType := flag.String("transport", "yandex", "Transport backend (yandex, oneme)")
	docURL := flag.String("url", "", "Document URL (Yandex Docs transport)")
	maxToken := flag.String("maxToken", "", "MAX web auth token (MAX transport)")
	maxUID := flag.String("maxUid", "", "MAX callee user ID (MAX transport)")
	flag.Parse()

	setupLogging(*debug)

	if *exitNode == *client {
		slog.Error("exactly one of --exit-node or --client must be set")
		flag.Usage()
		os.Exit(1)
	}

	slog.Info("ferrywire starting", "mode", modeName(*exitNode), "transport", *transportType)

	trans, err := buildTransport(*transportType, *exitNode, *docURL, *maxToken, *maxUID)
	if err != nil {
		slog.Error("failed to build transport", "err", err)
		os.Exit(1)
	}

	if err := trans.Start(); err != nil {
		slog.Error("failed to start transport", "err", err)
		os.Exit(1)
	}

	tun := tunnel.NewTCPTunnel(trans, *exitNode)

	if *exitNode {
		slog.Info("running as exit node (needs root for raw socket)")
		waitForShutdown()
		return
	}

	slog.Info("running as client", "socks5", *socksAddr)
	server := socks5.NewSOCKS5Server(*socksAddr, tun)
	go func() {
		if err := server.Start(); err != nil {
			slog.Error("socks5 server stopped", "err", err)
			os.Exit(1)
		}
	}()
	waitForShutdown()
}

func buildTransport(kind string, exitNode bool, docURL, maxToken, maxUID string) (transport.Transport, error) {
	config := transport.DefaultConfig()
	switch kind {
	case "yandex":
		return transport.NewCompressedTransport(yandex.NewYandexDocsTransport(docURL, config)), nil
	case "oneme":
		uid, err := strconv.ParseInt(maxUID, 10, 64)
		if err != nil && !exitNode {
			return nil, fmt.Errorf("invalid --maxUid %q: %w", maxUID, err)
		}
		return transport.NewCompressedTransport(oneme.NewOneMeTransport(exitNode, maxToken, uid, config)), nil
	default:
		return nil, fmt.Errorf("unknown transport type: %s", kind)
	}
}

func modeName(exitNode bool) string {
	if exitNode {
		return "exit-node"
	}
	return "client"
}

func setupLogging(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func waitForShutdown() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	received := <-sig
	slog.Info("shutting down", "signal", received.String())
}
