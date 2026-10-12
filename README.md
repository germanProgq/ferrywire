# ferrywire

Network-stack research tool: a TCP tunnel with pluggable transports.

```
Client (SOCKS5) --> Transport --> Exit node --> Internet
```

The client runs a SOCKS5 proxy and tunnels TCP through a transport. The exit
node decapsulates packets and forwards them to their destination via a raw
socket.

## Transports

- `yandex` - carries packets inside Yandex Docs editor cursor messages.
- `oneme` - carries packets over a MAX messenger WebRTC data channel.

## Requirements

- Go 1.26.4+ for the desktop client / exit-node binary.
- Android NDK r27+ for the Android client build.
- Xcode 26+ for the iOS client build.
- A Linux VPS with root for the exit node.

## Build

```bash
go build -o ferrywire .
```

Android and iOS helper scripts build client artifacts:

```bash
export ANDROID_NDK_HOME=<your Android NDK path>
./build_android.sh

export XCODE_PATH="<your Xcode.app path>"   # optional, defaults to /Applications/Xcode.app
./build_ios.sh
```

The iOS script requires a cgo export surface (`//export` functions), which the
current sources do not define; it aborts with guidance until one is added.

## Usage

Exit node (needs root; on Linux it auto-installs an `iptables` OUTPUT rule that
drops outbound RSTs so the kernel does not tear down tunneled connections):

```bash
sudo ./ferrywire --exit-node --transport yandex --url "YOUR_YANDEX_DOC_URL" --debug
```

If the automatic `iptables` rule fails, add it manually:

```bash
sudo iptables -I OUTPUT 1 -p tcp --tcp-flags RST RST -j DROP
```

Client (exposes a SOCKS5 proxy on 127.0.0.1:1080 by default):

```bash
./ferrywire --client --transport yandex --url "YOUR_YANDEX_DOC_URL" --socks5 127.0.0.1:1080 --debug
```

Point your browser's SOCKS5 proxy at the client's listen address.

## Flags

| Flag          | Default            | Description                          |
|---------------|--------------------|--------------------------------------|
| `--exit-node` | `false`            | Run as exit node (needs root)        |
| `--client`    | `false`            | Run as client                        |
| `--transport` | `yandex`           | Transport backend (`yandex`, `oneme`)|
| `--socks5`    | `127.0.0.1:1080`   | SOCKS5 listen address                |
| `--url`       | ``                 | Document URL (Yandex Docs transport) |
| `--maxToken`  | ``                 | MAX web auth token (MAX transport)   |
| `--maxUid`    | ``                 | MAX callee user ID (MAX transport)   |
| `--debug`     | `false`            | Enable verbose debug logging         |

Exactly one of `--exit-node` or `--client` must be set.

## Layout

```
main.go              CLI, logging, wiring
socks5/              SOCKS5 server
transport/           transport interface + lz4 compression wrapper
transport/yandex/    Yandex Docs transport
transport/oneme/     MAX messenger transport
tunnel/              gVisor TCP tunnel, raw-socket exit endpoint, checksums
```

## Custom transports

Implement the `Transport` interface from `transport/transport.go` and register
it in the `main.go` transport switch.
