package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"snmptrap-relay/internal/ber"
	"snmptrap-relay/internal/engine"
	"snmptrap-relay/internal/forward"
	"snmptrap-relay/internal/model"
	"snmptrap-relay/internal/receiver"
	"snmptrap-relay/internal/server"
)

type result struct {
	TargetTPS    int                    `json:"target_tps"`
	Sent         uint64                 `json:"sent"`
	Received     uint64                 `json:"received"`
	Forwarded    uint64                 `json:"forwarded"`
	QueueDropped uint64                 `json:"queue_dropped"`
	ParseFailed  uint64                 `json:"parse_failed"`
	HandleFailed uint64                 `json:"handle_failed"`
	Accepted     uint64                 `json:"accepted"`
	ElapsedSecs  float64                `json:"elapsed_seconds"`
	LossFree     bool                   `json:"loss_free"`
	Metrics      server.MetricsSnapshot `json:"metrics"`
}

func main() {
	var (
		duration   = flag.Duration("duration", 5*time.Second, "duration for each test step")
		rateList   = flag.String("rates", "5000,10000,20000,40000,80000,120000,160000", "comma-separated target TPS values")
		queueSize  = flag.Int("queue-size", 4096, "server queue size")
		workers    = flag.Int("workers", 2, "server worker count")
		maxDedupe  = flag.Int("max-dedup-entries", 10000, "max dedup entries")
		packetSize = flag.Int("max-datagram-size", 8192, "server max datagram size")
		profile    = flag.String("profile", "dedup-disabled", "traffic profile: dedup-disabled or dedup-clear")
		pairCount  = flag.Int("pair-count", 50, "number of dedup/clear key pairs for the dedup-clear profile")
		jsonOut    = flag.Bool("json", true, "emit JSON results")
	)
	flag.Parse()

	rates, err := parseRates(*rateList)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid rates: %v\n", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	listenPort, err := reserveUDPPort()
	if err != nil {
		fmt.Fprintf(os.Stderr, "listen port error: %v\n", err)
		os.Exit(1)
	}
	forwardListener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		fmt.Fprintf(os.Stderr, "forward listener error: %v\n", err)
		os.Exit(1)
	}
	defer forwardListener.Close()
	go drainUDP(forwardListener)

	cfg := &model.AppConfig{
		Server: model.ServerConfig{
			Host:                   "127.0.0.1",
			Port:                   listenPort,
			MaxDatagramSize:        *packetSize,
			CleanupIntervalSeconds: 30,
			MaxDedupEntries:        *maxDedupe,
			QueueSize:              *queueSize,
			WorkerCount:            *workers,
			StatsLogIntervalSecs:   0,
		},
		Forwarders: []model.ForwarderConfig{{
			Name:       "perf-target",
			Host:       "127.0.0.1",
			Port:       forwardListener.LocalAddr().(*net.UDPAddr).Port,
			Enabled:    true,
			SourceHost: "127.0.0.1",
		}},
		Filters: model.FiltersConfig{DefaultAction: "keep"},
		Alarms: []model.AlarmRuleConfig{{
			ID:    "link_down",
			Match: model.MatchSpec{Raw: map[string]any{"trap_oid": alarmTrapOID}},
			Dedup: dedupConfigForProfile(*profile),
		}},
		FieldAliases: map[string]string{"1.3.6.1.4.1.9999.1.1": "device_id", "1.3.6.1.4.1.9999.1.2": "ifIndex"},
	}

	forwarder, err := forward.NewUDP(cfg.Forwarders)
	if err != nil {
		fmt.Fprintf(os.Stderr, "forwarder init error: %v\n", err)
		os.Exit(1)
	}
	defer forwarder.Close()

	decoder, err := receiver.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decoder init error: %v\n", err)
		os.Exit(1)
	}

	eng := engine.New(cfg, forwarder, decoder, logger, nil)
	srv, err := server.New(cfg.Server, eng, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "server init error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()
	defer func() {
		cancel()
		<-errCh
	}()

	serverAddr := net.JoinHostPort(cfg.Server.Host, fmt.Sprintf("%d", cfg.Server.Port))
	payloads, err := buildPayloadPool(*profile, 4096, *pairCount)
	if err != nil {
		fmt.Fprintf(os.Stderr, "payload profile error: %v\n", err)
		os.Exit(2)
	}

	var results []result
	for _, rate := range rates {
		res, err := runStep(serverAddr, payloads, rate, *duration, srv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rate %d failed: %v\n", rate, err)
			os.Exit(1)
		}
		results = append(results, res)
	}

	out := map[string]any{
		"go_version":        runtime.Version(),
		"gomaxprocs":        runtime.GOMAXPROCS(0),
		"gomemlimit":        os.Getenv("GOMEMLIMIT"),
		"profile":           *profile,
		"pair_count":        *pairCount,
		"queue_size":        *queueSize,
		"worker_count":      *workers,
		"max_dedup_entries": *maxDedupe,
		"rates":             results,
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(out)
		return
	}
	for _, res := range results {
		fmt.Printf("target=%d sent=%d received=%d forwarded=%d queue_dropped=%d loss_free=%t\n", res.TargetTPS, res.Sent, res.Received, res.Forwarded, res.QueueDropped, res.LossFree)
	}
}

func runStep(serverAddr string, payloads [][]byte, targetTPS int, duration time.Duration, srv *server.Server) (result, error) {
	before := srv.MetricsSnapshot()
	start := time.Now()
	deadline := start.Add(duration)
	var sent atomic.Uint64
	workers := 4
	perTick := targetTPS / 100
	if perTick == 0 {
		perTick = 1
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			conn, err := net.Dial("udp4", serverAddr)
			if err != nil {
				return
			}
			defer conn.Close()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			payloadIndex := offset
			for now := range ticker.C {
				if now.After(deadline) {
					return
				}
				for j := offset; j < perTick; j += workers {
					payload := payloads[payloadIndex%len(payloads)]
					payloadIndex++
					if _, err := conn.Write(payload); err == nil {
						sent.Add(1)
					}
				}
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(750 * time.Millisecond)
	after := srv.MetricsSnapshot()
	elapsed := time.Since(start).Seconds()
	res := result{
		TargetTPS:    targetTPS,
		Sent:         sent.Load(),
		Received:     after.Received - before.Received,
		Forwarded:    after.Forwarded - before.Forwarded,
		QueueDropped: after.QueueDropped - before.QueueDropped,
		ParseFailed:  after.ParseFailed - before.ParseFailed,
		HandleFailed: after.HandleFailed - before.HandleFailed,
		Accepted:     after.Accepted - before.Accepted,
		ElapsedSecs:  elapsed,
		LossFree:     (after.QueueDropped-before.QueueDropped) == 0 && (after.Received-before.Received) >= sent.Load(),
		Metrics: server.MetricsSnapshot{
			Received:      after.Received - before.Received,
			QueueDropped:  after.QueueDropped - before.QueueDropped,
			QueueDepth:    after.QueueDepth,
			QueueCapacity: after.QueueCapacity,
			ParseFailed:   after.ParseFailed - before.ParseFailed,
			HandleFailed:  after.HandleFailed - before.HandleFailed,
			Accepted:      after.Accepted - before.Accepted,
			Forwarded:     after.Forwarded - before.Forwarded,
			Filtered:      after.Filtered - before.Filtered,
			Duplicates:    after.Duplicates - before.Duplicates,
			PassThrough:   after.PassThrough - before.PassThrough,
			Alarms:        after.Alarms - before.Alarms,
			DedupDisabled: after.DedupDisabled - before.DedupDisabled,
			ForwardFailed: after.ForwardFailed - before.ForwardFailed,
			WorkerCount:   after.WorkerCount,
		},
	}
	return res, nil
}

func parseRates(input string) ([]int, error) {
	parts := strings.Split(input, ",")
	rates := make([]int, 0, len(parts))
	for _, part := range parts {
		var value int
		_, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &value)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("invalid rate %q", part)
		}
		rates = append(rates, value)
	}
	sort.Ints(rates)
	return rates, nil
}

func drainUDP(conn *net.UDPConn) {
	buf := make([]byte, 8192)
	for {
		if _, _, err := conn.ReadFromUDP(buf); err != nil {
			return
		}
	}
}

func buildPayloadPool(profile string, size int, pairCount int) ([][]byte, error) {
	if pairCount <= 0 {
		return nil, fmt.Errorf("pair-count must be positive")
	}
	switch profile {
	case "dedup-disabled":
		return buildDedupDisabledPayloadPool(size), nil
	case "dedup-clear":
		return buildDedupClearPayloadPool(size, pairCount), nil
	default:
		return nil, fmt.Errorf("unsupported profile %q", profile)
	}
}

const (
	alarmTrapOID = "1.3.6.1.4.1.9999.0.10"
	clearTrapOID = "1.3.6.1.4.1.9999.0.11"
	deviceIDOID  = "1.3.6.1.4.1.9999.1.1"
	ifIndexOID   = "1.3.6.1.4.1.9999.1.2"
)

func dedupConfigForProfile(profile string) model.DedupConfig {
	switch profile {
	case "dedup-clear":
		return model.DedupConfig{
			TTLSeconds:     300,
			KeyFields:      []string{"fields.ifIndex", "fields.device_id"},
			HoldUntilClear: true,
			Clear: &model.DedupClearConfig{
				Match:     model.MatchSpec{Raw: map[string]any{"trap_oid": clearTrapOID}},
				KeyFields: []string{"fields.ifIndex", "fields.device_id"},
			},
		}
	default:
		return model.DedupConfig{TTLSeconds: 300, KeyFields: []string{"fields.synthetic_unique_id"}}
	}
}

func buildDedupDisabledPayloadPool(size int) [][]byte {
	payloads := make([][]byte, 0, size)
	for i := 0; i < size; i++ {
		payloads = append(payloads, buildTrapPayload(alarmTrapOID, fmt.Sprintf("device-%04d", i), int64(i+1), int64(12345+i)))
	}
	return payloads
}

func buildDedupClearPayloadPool(size int, pairCount int) [][]byte {
	pattern := make([][]byte, 0, pairCount*4)
	for i := 0; i < pairCount; i++ {
		deviceID := fmt.Sprintf("device-%02d", i)
		ifIndex := int64(i + 1)
		timeBase := int64(100000 + i*10)
		pattern = append(pattern,
			buildTrapPayload(alarmTrapOID, deviceID, ifIndex, timeBase),
			buildTrapPayload(alarmTrapOID, deviceID, ifIndex, timeBase+1),
			buildTrapPayload(clearTrapOID, deviceID, ifIndex, timeBase+2),
			buildTrapPayload(alarmTrapOID, deviceID, ifIndex, timeBase+3),
		)
	}
	payloads := make([][]byte, 0, size)
	for len(payloads) < size {
		payloads = append(payloads, pattern[len(payloads)%len(pattern)])
	}
	return payloads
}

func buildTrapPayload(trapOID string, deviceID string, ifIndex int64, uptime int64) []byte {
	return buildV2Trap("public", []varBind{
		{OID: "1.3.6.1.2.1.1.3.0", Value: tlv(0x43, encodeIntegerBytes(uptime))},
		{OID: "1.3.6.1.6.3.1.1.4.1.0", Value: encOID(trapOID)},
		{OID: deviceIDOID, Value: tlv(0x04, []byte(deviceID))},
		{OID: ifIndexOID, Value: tlv(0x02, encodeIntegerBytes(ifIndex))},
	})
}

func reserveUDPPort() (int, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).Port, nil
}

type varBind struct {
	OID   string
	Value []byte
}

func buildV2Trap(community string, binds []varBind) []byte {
	var vbSeq [][]byte
	for _, vb := range binds {
		vbSeq = append(vbSeq, encodeVarBind(vb.OID, vb.Value))
	}
	varbindList := seq(vbSeq...)
	var pduBody []byte
	pduBody = append(pduBody, tlv(0x02, encodeIntegerBytes(1))...)
	pduBody = append(pduBody, tlv(0x02, encodeIntegerBytes(0))...)
	pduBody = append(pduBody, tlv(0x02, encodeIntegerBytes(0))...)
	pduBody = append(pduBody, varbindList...)
	pdu := tlv(0xA7, pduBody)
	return seq(
		tlv(0x02, encodeIntegerBytes(1)),
		tlv(0x04, []byte(community)),
		pdu,
	)
}

func encodeVarBind(oid string, value []byte) []byte {
	return seq(encOID(oid), value)
}

func encOID(oid string) []byte {
	encoded, err := encodeOID(oid)
	if err != nil {
		panic(err)
	}
	return tlv(0x06, encoded)
}

func encodeOID(oid string) ([]byte, error) {
	parts := strings.Split(oid, ".")
	if len(parts) < 2 {
		return nil, &ber.Error{Msg: "OID needs at least two nodes"}
	}
	first := mustAtoi(parts[0])
	second := mustAtoi(parts[1])
	buf := []byte{byte(first*40 + second)}
	for _, part := range parts[2:] {
		n := mustAtoi(part)
		buf = append(buf, encodeBase128(n)...)
	}
	return buf, nil
}

func encodeBase128(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var tmp []byte
	for n > 0 {
		tmp = append([]byte{byte(n & 0x7F)}, tmp...)
		n >>= 7
	}
	for i := 0; i < len(tmp)-1; i++ {
		tmp[i] |= 0x80
	}
	return tmp
}

func mustAtoi(s string) int {
	n := 0
	for _, ch := range s {
		n = n*10 + int(ch-'0')
	}
	return n
}

func encodeIntegerBytes(n int64) []byte {
	if n == 0 {
		return []byte{0}
	}
	negative := n < 0
	var buf []byte
	for n != 0 && n != -1 {
		buf = append([]byte{byte(n & 0xFF)}, buf...)
		n >>= 8
	}
	if negative && buf[0]&0x80 == 0 {
		buf = append([]byte{0xFF}, buf...)
	}
	if !negative && buf[0]&0x80 != 0 {
		buf = append([]byte{0x00}, buf...)
	}
	return buf
}

func tlv(tag byte, value []byte) []byte {
	return append([]byte{tag}, append(encodeLength(len(value)), value...)...)
}

func seq(parts ...[]byte) []byte {
	var body []byte
	for _, part := range parts {
		body = append(body, part...)
	}
	return tlv(0x30, body)
}

func encodeLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var tmp []byte
	for n > 0 {
		tmp = append([]byte{byte(n & 0xFF)}, tmp...)
		n >>= 8
	}
	return append([]byte{0x80 | byte(len(tmp))}, tmp...)
}
