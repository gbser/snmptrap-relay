# Installation Guide

This guide assumes Linux with `systemd`. The daemon also runs on other platforms, but the service example is Linux-specific.

## 1. Prerequisites

- Go 1.26 or newer on the build machine.
- Permission to bind the configured UDP port.

If you want to listen on UDP 162 as a non-root user, grant the binary the capability to bind privileged ports or redirect 162 to a higher port.

The destination node does not need Go installed. You can build the binary on one Linux machine, copy the compiled `snmptrap-relay` executable to the final node, and run it there directly as long as the CPU architecture matches.

## 2. Create an application directory

```bash
sudo mkdir -p /opt/snmptrap-relay
sudo mkdir -p /etc/snmptrap-relay
sudo mkdir -p /var/log/snmptrap-relay
sudo useradd --system --no-create-home --shell /usr/sbin/nologin snmprelay
sudo chown -R snmprelay:snmprelay /opt/snmptrap-relay /var/log/snmptrap-relay
```

## 3. Build the binary

```bash
cd /path/to/snmptrapd-filtering
go build -o /opt/snmptrap-relay/snmptrap-relay ./cmd/snmptrap-relay
```

If you prefer installing into your Go bin directory, use this only for local runs or PATH-based setups:

```bash
go install ./cmd/snmptrap-relay
```

If you build on one machine and deploy to another, copy the resulting binary and the config file to the destination node. The target system only needs the executable, the YAML config, and optionally the systemd unit file.

## 4. Copy and edit the configuration

```bash
cp config.example.yaml /etc/snmptrap-relay/config.yaml
```

If you want the relay to listen and forward over IPv6, start from `examples/ipv6.yaml` instead of `config.example.yaml`.

Edit the following sections first:

- `server.host`
- `server.port`
- `runtime.memory_limit` if you want an in-config Go soft memory target
- `forwarders`
- `field_aliases`
- `alarms` (including each alarm's `dedup.clear` block, if used)

## 5. Validate the configuration

```bash
/opt/snmptrap-relay/snmptrap-relay --config /etc/snmptrap-relay/config.yaml --check-config
```

## 6. Run the daemon manually

```bash
/opt/snmptrap-relay/snmptrap-relay --config /etc/snmptrap-relay/config.yaml
```

## 7. Install the systemd service

Copy the unit file:

```bash
sudo cp systemd/snmptrap-relay.service /etc/systemd/system/snmptrap-relay.service
sudo systemctl daemon-reload
sudo systemctl enable --now snmptrap-relay
```

Check status:

```bash
sudo systemctl status snmptrap-relay
```

Reload config without restart:

```bash
sudo systemctl reload snmptrap-relay
```

Optional memory controls:

- Set `runtime.memory_limit` in `/etc/snmptrap-relay/config.yaml` when you want the relay itself to configure the Go soft memory target.
- Set `Environment=GOMEMLIMIT=...` in the systemd unit if you prefer to keep the memory target outside the YAML file.
- Set `MemoryMax=...` in the systemd unit if you want a hard cgroup memory cap.

Example unit overrides:

```ini
[Service]
Environment=GOMEMLIMIT=128MiB
MemoryMax=192M
```

If you use both a Go soft limit and a systemd hard cap, keep `MemoryMax` above the Go limit.

## 8. Listener port notes

The default port is 162. You have three options:

1. Run the service with the `CAP_NET_BIND_SERVICE` capability.
2. Run the daemon as root.
3. Use a high port like 9162 and configure SNMP senders or firewall forwarding accordingly.

For most deployments, option 1 is preferred.

## 9. Integrate with snmptrapd

If traps already arrive in `snmptrapd`, configure it to forward matching notifications to this daemon instead of wiring the senders directly to `snmptrap-relay`.

Example `snmptrapd.conf` for SNMPv2c:

```conf
authCommunity log,net public
forward default udp:127.0.0.1:9161
```

Example `snmptrapd.conf` for SNMPv3:

```conf
createUser relayuser SHA auth-pass AES priv-pass
authUser log,net relayuser authPriv
forward default udp:127.0.0.1:9161
```

What this does:

- `authCommunity` or `authUser` allows `snmptrapd` to process the incoming traps.
- `forward default ...` sends the accepted traps to `snmptrap-relay`.
- The relay still performs its own filtering and deduplication before forwarding to the final receivers.

If you only want to use `snmptrapd` as the ingress point and keep all relay logic in this daemon, configure `snmptrap-relay` to listen on the forwarded port, for example `9161`.

## 10. Telegraf integration

Point Telegraf `inputs.snmp_trap` to the relay output port, not the senders.

Example:

```toml
[[inputs.snmp_trap]]
  service_address = "udp://:9162"
```

The daemon forwards accepted traps as SNMP UDP packets to that receiver. The trap payload is passed through unchanged; the relay only changes the UDP source address because it emits a new datagram.

## 11. SNMPv3 receiver configuration

If your senders use SNMPv3, populate `receiver.v3_users` in the YAML config.

Example:

```yaml
receiver:
  v3_users:
    - user_name: nms-traps
      authentication_protocol: sha256
      authentication_passphrase: trap-auth-secret
      privacy_protocol: aes256
      privacy_passphrase: trap-priv-secret
```

The receiver can contain multiple users. Reload the config with `SIGHUP` or `systemctl reload` after editing the file.
