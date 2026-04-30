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
- Supports an optional per-forwarder outbound source IP for multi-homed IPv4 or IPv6 hosts.
- Forwards accepted traps as raw SNMP UDP datagrams to one or more receivers.
- Logs what was filtered, deduplicated, cleared or forwarded.
- Logs `trap_queue_full` when the in-process queue is saturated and emits periodic `server_stats` counters.
- Can expose Prometheus-compatible metrics on an optional HTTP endpoint.
- Exposes `GET /healthz` on the metrics listener for lightweight health checks.
- Creates a log in snmptrapd format so it can be parsed by SNMP Exporter for Prometheus/Grafana.

## Project layout

- `cmd/snmptrap-relay/` contains the entry point.
- `internal/` contains the daemon implementation.
- `config.example.yaml` is a ready-to-edit configuration sample.
- `docs/architecture.md` explains the component flow.
- `docs/configuration.md` documents every configuration parameter.
- `docs/install.md` describes installation and service setup.
- `examples/production.yaml` is a more complete production-style config.
- `examples/ipv6.yaml` is a dedicated IPv6 listener and forwarding example.
- `systemd/snmptrap-relay.service` is a sample systemd unit.

## Quick start

1. Build the binary with `go build` on any machine that has Go installed.
2. Copy `config.example.yaml` to your runtime location.
3. Adjust the listen port, forwarders, aliases, rules and embedded clear traps.
4. Start the daemon with `snmptrap-relay --config /path/to/config.yaml`.

If you want an IPv6-native starting point, use `examples/ipv6.yaml` instead of the default IPv4-oriented sample.

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
- `runtime`: optional Go soft memory limit for this process.
- `metrics`: optional Prometheus listener, scrape path and `/healthz` endpoint.
- `logging`: level, format and optional file path.
- `receiver`: optional SNMPv3 users for authenticated trap decoding.
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

Example Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: snmptrap-relay
    static_configs:
      - targets:
          - 127.0.0.1:9163
    metrics_path: /metrics
```

The same listener exposes `GET /healthz` for simple probes.

Metrics configuration is hot-reloadable on `SIGHUP`. If a new metrics listener cannot be started, the relay keeps the previous metrics endpoint active and logs the reload failure instead of dropping observability.

### Memory limit configuration

You can set a Go soft memory limit directly in the YAML config:

```yaml
runtime:
  memory_limit: 128MiB
```

Supported examples include `64MiB`, `128MiB`, `256MiB`, plain byte values such as `67108864`, and `off` to disable a previously configured in-process limit.

If you use `systemd`, you can also set:

- `Environment=GOMEMLIMIT=128MiB` for a Go runtime soft limit outside the YAML config
- `MemoryMax=192M` for a hard cgroup limit enforced by `systemd`

If both are used, keep `MemoryMax` higher than `runtime.memory_limit` so the Go runtime has headroom to stay below the hard cap.

## Operational behavior

### Invalid or malformed traps

Malformed or unsupported SNMP datagrams are dropped for that packet only.

- The relay increments `parse_failed`.
- It logs `trap_parse_failed` with the sender address and parser error.
- It does not forward that trap.
- The process continues handling subsequent traffic.

If a trap parses successfully but later fails during evaluation or forwarding, the relay logs `trap_handle_failed` and increments `handle_failed`.

### Queue saturation and overload

The relay reads UDP packets into a bounded in-process queue controlled by:

- `server.queue_size`
- `server.worker_count`

When the queue fills:

- the new trap is dropped
- `trap_queue_full` is logged
- `queue_dropped` increases

If `server.stats_log_interval_seconds` is greater than `0`, the relay periodically logs `server_stats` with cumulative counters such as received, dropped, parse failures, forwarded events, duplicates and alarms.

### Forwarding and source address behavior

- The relay forwards the original SNMP payload unchanged.
- The downstream receiver sees the relay host as the UDP sender, not the original device.
- `forwarders[].source_host` can bind the outbound socket to a specific local IPv4 or IPv6 address on multi-homed hosts.

### IPv6 support

The relay supports IPv6 listeners and IPv6 forward targets. A ready-to-use example is provided in `examples/ipv6.yaml`.

## Metrics

When `metrics.enabled: true`, the relay exposes Prometheus metrics on `metrics.host:metrics.port` at `metrics.path`, plus `GET /healthz` on the same listener.

### Counters

- `snmptrap_relay_received_total`: total UDP traps read from the listening socket.
- `snmptrap_relay_queue_dropped_total`: total traps dropped because the internal queue was full.
- `snmptrap_relay_parse_failed_total`: total traps that could not be parsed.
- `snmptrap_relay_handle_failed_total`: total traps that parsed but failed later during handling.
- `snmptrap_relay_accepted_total`: total events accepted by the engine.
- `snmptrap_relay_forwarded_total`: total events actually forwarded to downstream receivers.
- `snmptrap_relay_filtered_total`: total events dropped by filter rules.
- `snmptrap_relay_duplicates_total`: total events suppressed by deduplication.
- `snmptrap_relay_pass_through_total`: total events forwarded without matching an alarm. This also includes clear traps that are passed through.
- `snmptrap_relay_alarms_total`: total first-seen alarm events forwarded as alarms.
- `snmptrap_relay_dedup_disabled_total`: total events forwarded when dedup could not be applied because required key fields were missing.
- `snmptrap_relay_forward_failed_total`: total events whose outbound forwarding failed.

### Gauges

- `snmptrap_relay_queue_depth`: current number of traps waiting in the internal queue.
- `snmptrap_relay_queue_capacity`: configured internal queue size.
- `snmptrap_relay_worker_count`: configured number of processing workers.

### Interpreting the metrics

- Rising `queue_depth` together with increasing `queue_dropped_total` means the relay is overloaded.
- Rising `parse_failed_total` means malformed or unsupported traps are arriving.
- Rising `duplicates_total` means dedup is actively suppressing repeated alarms.
- Rising `forward_failed_total` means the relay accepted traffic but could not send it to at least one target.

## Capacity and sizing

Memory usage is mainly driven by:

- `runtime.memory_limit` when configured
- `server.queue_size * server.max_datagram_size`
- `server.max_dedup_entries`
- normal Go runtime overhead

Practical sizing guidance for the current defaults:

- `32 MiB`: lab-only or very low traffic
- `64 MiB`: small deployments with limited headroom
- `128 MiB`: recommended starting point for the default config
- `256 MiB`: recommended for the production example or burstier environments

As a rule of thumb:

- every additional 1024 queue slots at 8192 bytes adds about 8 MiB of queue payload capacity
- every additional 10000 dedup states adds roughly 5 to 15 MiB depending on key sizes

## Measured local throughput

The repository includes a local performance harness in `cmd/snmptrap-relay-perf/` for end-to-end UDP testing.

On an Apple M4 host with 10 CPU cores and 24 GiB RAM, using local UDP ingress and a local UDP sink:

- with a dedup-disabled fast path, the relay remained loss-free up to about 49k offered traps/s with no Go memory limit
- with `GOMEMLIMIT=64MiB`, the same fast path remained loss-free up to about 35k offered traps/s
- with a 50-key dedup-and-clear workload under `GOMEMLIMIT=64MiB`, the relay remained loss-free up to about 54k offered traps/s and started dropping at about 55k offered traps/s

Those numbers are local processing ceilings for the tested profile, not universal production guarantees. Real downstream latency, disk logging, larger traps, more complex rule sets, and remote forwarding targets will reduce the safe operating rate.

## Notes

- Dedup state is memory-only by design.
- If the daemon restarts, all dedup windows are lost.
- The downstream receiver sees the relay as the UDP sender, not the original source IP.
- The trap payload itself is forwarded unchanged; only the transport source address changes.
- The original sender IP is still logged and can be used in the dedup key via `source_ip` if required.
- SNMPv3 support is enabled by configuring `receiver.v3_users`.
- Logs go to stdout by default or to `logging.file` when configured. Structured output supports `text` and `json` formats.
- `logging.alerts_file` can be used to write an alerts-only log in snmptrapd-compatible text format for downstream parsing.
- Malformed-trap warnings are logged at `WARN`. If you run at `ERROR` level, they still increment `parse_failed` but will not appear in the log output.
