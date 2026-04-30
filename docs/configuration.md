# Configuration Reference

This file describes every configuration section and parameter supported by the relay.

## Top-level sections

- `server`
- `runtime`
- `metrics`
- `logging`
- `receiver`
- `field_aliases`
- `forwarders`
- `filters`
- `dedup_defaults`
- `alarms`

## `server`

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `host` | string | `0.0.0.0` | UDP listen address. |
| `port` | integer | `162` | UDP listen port. Use a privileged port or a high port with firewall/NAT forwarding. |
| `max_datagram_size` | integer | `8192` | Maximum packet size accepted by the listener. |
| `cleanup_interval_seconds` | integer | `30` | Interval for dedup cleanup. |
| `max_dedup_entries` | integer | `10000` | Maximum in-memory dedup states retained before the oldest state is evicted. |
| `queue_size` | integer | `1024` | Size of the in-process trap queue between UDP reads and workers. When full, new traps are dropped and a warning is logged. |
| `worker_count` | integer | `1` | Number of trap processing workers consuming the queue. |
| `stats_log_interval_seconds` | integer | `60` | Interval for periodic `server_stats` log entries. Set to `0` to disable periodic stats logging. |

## `runtime`

Optional Go runtime process controls.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `memory_limit` | string or null | null | Optional Go soft memory limit for the process. Examples: `64MiB`, `128MiB`, `256MiB`, raw bytes such as `67108864`, or `off` to disable a previously configured in-process limit. |

Example:

```yaml
runtime:
  memory_limit: 128MiB
```

This setting applies `runtime/debug.SetMemoryLimit` at startup and on config reload. It controls the Go runtime soft memory target. It is not the same as a hard operating system or cgroup limit.

If you run under `systemd`, you may also set:

- `Environment=GOMEMLIMIT=128MiB` for an equivalent Go soft limit outside YAML
- `MemoryMax=192M` for a hard cgroup limit

When both are used, keep the hard `MemoryMax` above `runtime.memory_limit`.

## `logging`

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `level` | string | `INFO` | Logging level. |
| `format` | string | `text` | `text` or `json`. |
| `file` | string or null | null | Optional log file path. If omitted, logs go to stdout/stderr. |
| `alerts_file` | string or null | null | Optional alerts-only log file path. Log files created by the relay use mode `0600`. |

## `metrics`

Optional Prometheus endpoint configuration.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `enabled` | boolean | `false` | Enables the Prometheus metrics HTTP endpoint. |
| `host` | string | `127.0.0.1` | Listen address for the metrics HTTP server. |
| `port` | integer | `9163` | Listen port for the metrics HTTP server. |
| `path` | string | `/metrics` | HTTP path serving Prometheus metrics. |

Example:

```yaml
metrics:
  enabled: true
  host: 127.0.0.1
  port: 9163
  path: /metrics
```

Changes to `metrics.*` are applied on config reload. If the new metrics listener cannot be started, the relay keeps the previous metrics configuration and logs the reload failure.
The same listener also serves a fixed `GET /healthz` endpoint that returns `200 OK` with `ok`.

## `receiver`

The `receiver` section configures SNMPv3 decoding. If no users are configured, the relay still accepts SNMPv1/v2c traps.

### `receiver.v3_users`

List of SNMPv3 users accepted by the relay.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `user_name` | string | required | SNMPv3 USM user name. |
| `authentication_protocol` | string | `noauth` | One of `noauth`, `md5`, `sha`, `sha224`, `sha256`, `sha384`, `sha512`. |
| `authentication_passphrase` | string | empty | Authentication passphrase. |
| `privacy_protocol` | string | `nopriv` | One of `nopriv`, `des`, `aes`, `aes192`, `aes256`, `aes192c`, `aes256c`. |
| `privacy_passphrase` | string | empty | Privacy passphrase. |

## `field_aliases`

Mapping from SNMP OID to a friendly field name.
OID keys may be written with or without a leading dot. The relay normalizes them internally, so one alias per OID is enough.

Example:

```yaml
field_aliases:
  "1.3.6.1.2.1.2.2.1.1": ifIndex
  "1.3.6.1.4.1.9999.1.1": device_id
```

These aliases become available as:

- `fields.ifIndex`
- `fields.device_id`

They can be used in matches and dedup key fields.

## `forwarders`

List of SNMP receivers that receive accepted traps.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `name` | string | required | Human-friendly destination name used in logs. |
| `host` | string | required | Receiver host or IP. |
| `port` | integer | required | Receiver UDP port. |
| `enabled` | boolean | `true` | Disable a target without deleting it. |
| `source_host` | string or null | null | Optional local source IP address to bind for outbound traps. Useful on multi-homed IPv4 or IPv6 hosts. |

Example:

```yaml
forwarders:
  - name: telegraf
    host: 127.0.0.1
    port: 9162
    source_host: 127.0.0.1
```

Forwarders receive the original SNMP trap datagram as-is. The relay does not rewrite OIDs, varbinds or other payload fields before sending the packet onward.
When `source_host` is set, the relay binds the outbound socket to that local address before forwarding.

## `filters`

Block/keep rules are evaluated before deduplication.

```yaml
filters:
  default_action: keep
  rules:
    - id: drop_test
      action: drop
      match:
        trap_oid: "1.3.6.1.4.1.9999.0.1"
```

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `default_action` | string | `keep` | Action used when no rule matches. `keep` accepts the trap, `drop` discards it. |
| `rules` | list | empty | Ordered list of filter rules. The first matching rule wins. |

Each rule has:

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `id` | string | required | Rule name used in logs and debug output. |
| `action` | string | `drop` | `drop` blocks the trap. `keep` allows the trap to continue to dedup and forwarding. |
| `match` | mapping | required | Rule matching expression. |

Filter behavior:

- `keep` means "accept this trap".
- `drop` means "ignore this trap".
- The engine stops at the first matching rule.
- If no rule matches, `default_action` is applied.
- A `keep` rule is useful when you want to whitelist a subset of traps before a broader drop policy.

Example:

```yaml
filters:
  default_action: drop
  rules:
    - id: keep_prod_traps
      action: keep
      match:
        source_ip: "10.10.10.5"
    - id: drop_test_traps
      action: drop
      match:
        trap_oid: "1.3.6.1.4.1.9999.0.1"
```

## `dedup_defaults`

Optional default dedup settings inherited by alarm rules.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `ttl_seconds` | integer | `300` | Default dedup window. |
| `key_fields` | list of strings | empty | Default key fields. |

## `alarms`

Alarm rules define what gets deduplicated.

| Key | Type | Description |
| --- | --- | --- |
| `id` | string | Alarm name. Used in logs and as the dedup namespace. |
| `match` | mapping | Selects trap types that belong to this alarm. |
| `dedup` | mapping | Per-alarm dedup settings. |

Example:

```yaml
alarms:
  - id: link_down
    match:
      trap_oid: "1.3.6.1.4.1.9999.0.10"
    dedup:
      ttl_seconds: 300
      key_fields:
        - trap_oid
        - fields.ifIndex
        - fields.device_id
      clear:
        match:
          trap_oid: "1.3.6.1.4.1.9999.0.11"
        key_fields:
          - trap_oid
          - fields.ifIndex
          - fields.device_id
```

### `alarm.dedup`

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `ttl_seconds` | integer | inherited | TTL for the dedup window. |
| `key_fields` | list of strings | inherited | Fields used to build the dedup key. |
| `hold_until_clear` | boolean | `false` | If `true`, the dedup state never expires on its own and is removed only by the alarm's clear trap. |
| `clear` | mapping or null | null | Optional clear-trap definition that resets this alarm's dedup state. |

### `alarm.dedup.clear`

The `clear` block lives under the alarm it resets, so large configs keep the alarm and its clear trap together.
If several clear trap OIDs should reset the same alarm, use `match.any` inside this block.
Set `hold_until_clear: true` when you want the alarm to stay suppressed until the clear trap arrives, regardless of `ttl_seconds`.

| Key | Type | Default | Description |
| --- | --- | --- | --- |
| `match` | mapping | required | Trap match expression for the clear event. |
| `key_fields` | list of strings or null | null | Optional key fields to use for the clear trap. If omitted, the parent alarm's `key_fields` are reused. When `trap_oid` is included and the clear uses a different OID, the relay uses the parent alarm's configured `trap_oid` value for key matching so the clear still resets the original alarm. |

This supports both clear styles:

- Same alarm and clear OID, with `varbind_oid` + `regex` to distinguish the clear event.
- Different alarm and clear OIDs, while still keeping `trap_oid` inside the dedup key to avoid cross-deduplicating unrelated alarms.

## Match syntax

You can use a simple mapping:

```yaml
match:
  trap_oid: "1.3.6.1.4.1.9999.0.10"
  source_ip: "10.0.0.5"
```

This means all fields must match exactly.

For advanced logic, use `all`, `any` and `not`:

```yaml
match:
  all:
    - field: trap_oid
      op: eq
      value: "1.3.6.1.4.1.9999.0.10"
    - field: fields.ifIndex
      op: exists
  any:
    - field: source_ip
      op: prefix
      value: "10.0."
```

Supported operators:

- `exists`
- `eq`
- `ne`
- `contains`
- `prefix`
- `suffix`
- `regex`
- `in`
- `gt`
- `ge`
- `lt`
- `le`

Supported fields:

- `source_ip`
- `source_port`
- `version`
- `snmp_version`
- `community`
- `pdu_type`
- `enterprise_oid`
- `agent_address`
- `generic_trap`
- `generic_trap_name`
- `specific_trap`
- `uptime`
- `request_id`
- `error_status`
- `error_index`
- `trap_oid`
- `fields.<alias>`
- `varbind.<oid>`
- `varbind:<oid>`

Notes:

- `fields.<alias>` uses aliases defined in `field_aliases`.
- `varbind.<oid>` matches the decoded varbind value for an exact OID, with or without a leading dot.
- `varbind:<oid>` is accepted as an alternative form for the same lookup.
- `trap_oid` comparisons are normalized the same way, so `1.3...` and `.1.3...` both match.
- Numeric operators (`gt`, `ge`, `lt`, `le`) compare values as numbers when possible.
- `exists` checks that the resolved field is present and non-empty.

## Dedup behavior

If all configured key fields are present, a deterministic hash is built from them.

- If the key is new, the trap is forwarded.
- If the key already exists inside the TTL window, the trap is suppressed.
- If the alarm's embedded clear trap matches, the state is removed and the next alarm is forwarded again.
- If `hold_until_clear: true` is set, the trap remains suppressed indefinitely until a matching clear arrives or the process restarts.
- `server.max_dedup_entries` limits total in-memory dedup state. When the limit is reached, the least recently seen state is evicted first.
- `server_stats` log entries expose current in-process counters such as received traps, queue drops, parse failures, forwards, filters, and deduplications.
- If the in-process queue fills, the relay logs `trap_queue_full` and drops the newest trap instead of blocking the UDP read loop.

If a key field is missing from the trap, dedup is skipped for that event and a warning is logged.
