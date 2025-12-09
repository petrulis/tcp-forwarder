# TCP Forwarder

A TCP forwarder is a service that forwards TCP messages to connected clients.
Each client can send and receive messages. The service tracks bytes sent and
bytes received for each individual client. Once a client reaches a 100-byte limit,
the connection is closed.

## Architecture

The server use 1 buffered broadcast channel and N per-client channels to
handle backpressure due to slow clients or due to too many messages.
However, when a buffer gets clogged, the server starts dropping messages.
Slow client connections are tracked with an atomic counter that counts
how many messages were dropped for the particlar client. When a client
drops too many messages, it gets disconnected.

The server implements graceful shutdown which closes the connections gracefully
when SIGTERM or SIGINT is received.

Connection is wrapped with `limitedConn`  to enforce strict upload/download limits.
Uses mutexes - atomics should be more efficient under high load.
Once a client hits the limit, they get a goodbye message
and the connection closes.

Each connection is spawned in a goroutine which has recovery implemented to avoid
crashing the whole server. In turn, each connection runs read and write loops.

The file structure is chosen to be flat because it's just a demo, not a fully
flegged server implementation. The idea is to not jump into a structure before
it's needed.

## Usage

```bash
go build && ./tcp-forwarder
```

The server will start on `:9000`. Connect with multiple clients:

```bash
nc localhost 9000
```

Type messages. They get forwarded to all other connected clients.
