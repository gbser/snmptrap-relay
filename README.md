# SNMP Trap Relay

`snmptrap-relay` is a small Go daemon that sits between SNMP senders and SNMP receivers:

`SNMP sender -> daemon -> SNMP receiver`

It listens for traps, applies filtering and in-memory deduplication, logs every suppressed trap with the original first trap that started the dedup window, and forwards accepted traps to one or more SNMP receivers without rewriting the trap payload.

## What it does

- Receives SNMP v1/v2c traps over UDP.
- Receives SNMPv3 traps over UDP when `receiver.v3_users` is configured.
- Parses the trap enough to extract OIDs, varbinds and matching fields.
- Applies configurable block rules.
- Deduplicates alarms in memory only, with a TTL per alarm rule.
- Can keep an alarm suppressed until its embedded clear trap arrives when `dedup.hold_until_clear` is enabled.
- Supports clear traps nested under each alarm's dedup rule.
- Forwards accepted traps as raw SNMP UDP datagrams to one or more receivers.
- Logs what was filtered, deduplicated, cleared or forwarded.

## Project layout

- `cmd/snmptrap-relay/` contains the entry point.
- `internal/` contains the daemon implementation.
- `config.example.yaml` is a ready-to-edit configuration sample.
- `docs/architecture.md` explains the component flow.
- `docs/configuration.md` documents every configuration parameter.
- `docs/install.md` describes installation and service setup.
- `examples/production.yaml` is a more complete production-style config.
- `systemd/snmptrap-relay.service` is a sample systemd unit.

## Quick start

1. Build the binary with `go build` on any machine that has Go installed.
2. Copy `config.example.yaml` to your runtime location.
3. Adjust the listen port, forwarders, aliases, rules and embedded clear traps.
4. Start the daemon with `snmptrap-relay --config /path/to/config.yaml`.

The destination node does not need Go installed. You can build the binary on one machine and copy the compiled executable to the final node, as long as the OS and CPU architecture match.

If you already run `snmptrapd`, configure it to forward traps to the relay's UDP listener port using `snmptrapd.conf`:

```conf
authCommunity log,net public
forward default udp:127.0.0.1:9161
```

Reload config without restart:

```bash
kill -HUP $(pidof snmptrap-relay)
```

## Example workflow

1. Sender emits a trap.
2. Relay parses the trap.
3. Filter rules run first.
4. The matching alarm's embedded clear trap resets dedup state if it matches.
5. Alarm rules are evaluated in order.
6. First alarm instance is forwarded.
7. Repeated traps inside the TTL window are suppressed and logged.
8. A clear trap nested under the alarm deletes the stored dedup state so the next alarm is forwarded again.

## Configuration at a glance

Main sections:

- `server`: listen address, packet size and cleanup interval.
- `logging`: level, format and optional file path.
- `field_aliases`: OID-to-name mapping used in matches and key fields.
- `forwarders`: destination SNMP receivers.
- `filters`: ordered keep/drop rules evaluated before dedup.
- `dedup_defaults`: default TTL and key fields used by alarm rules.
- `alarms`: alarm definitions, dedup settings and embedded clear traps.

## Run

```bash
snmptrap-relay --config /etc/snmptrap-relay/config.yaml
```

Validate config without starting the daemon:

```bash
snmptrap-relay --config /etc/snmptrap-relay/config.yaml --check-config
```

## Notes

- Dedup state is memory-only by design.
- If the daemon restarts, all dedup windows are lost.
- The downstream receiver sees the relay as the UDP sender, not the original source IP.
- The trap payload itself is forwarded unchanged; only the transport source address changes.
- The original sender IP is still logged and can be used in the dedup key via `source_ip` if required.
- SNMPv3 support is enabled by configuring `receiver.v3_users`.
