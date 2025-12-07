# TCP Forwarder

A TCP forwarder is a service that forwards TCP messages to connected clients.
Each client can send and receive messages. The service tracks bytes sent and
bytes received for each individual client. Once a client reaches a 100-byte
upload limit, the connection is closed.