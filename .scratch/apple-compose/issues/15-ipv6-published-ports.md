# 15 Published ports on IPv6
Type: prototype
Status: resolved

## Question
The runtime publishes ports on IPv4 only, so a client that resolves localhost to ::1 and does not fall back is refused, where Docker answers on both. How should apple-compose close that gap?

## Answer
The per-project background supervisor also relays IPv6. On every pass it lists running containers, collects TCP ports published on every address, listens on [::] for each with IPV6_V6ONLY set, which a probe confirmed coexists with the runtime's IPv4 listener, and relays each connection to 127.0.0.1. Listeners close when a container stops publishing. Ports bound to a specific address and UDP ports are left alone. A project now needs the supervisor when it publishes such a port, attached `up` ensures it too, and apple-docker labels `run -p` containers and starts the supervisor before handing over to the runtime; a 30 second grace period covers the gap until the container exists, and applies only until the supervisor has found something to look after. apple-docker's pseudo project never loads a compose file, which it previously picked up from any directory above the state directory. APPLE_COMPOSE_IPV6_PORTS=off disables the relay.
