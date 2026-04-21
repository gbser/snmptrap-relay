package receiver

import (
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"

	"snmptrap-relay/internal/model"
	"snmptrap-relay/internal/oidutil"
	"snmptrap-relay/internal/snmp"
)

type Decoder interface {
	Decode(data []byte, sourceIP string, sourcePort int) (*model.TrapEvent, error)
}

type GosnmpDecoder struct {
	mu  sync.RWMutex
	g   *gosnmp.GoSNMP
	cfg *model.AppConfig
}

func New(cfg *model.AppConfig) (*GosnmpDecoder, error) {
	dec := &GosnmpDecoder{}
	if err := dec.Reload(cfg); err != nil {
		return nil, err
	}
	return dec, nil
}

func (d *GosnmpDecoder) Reload(cfg *model.AppConfig) error {
	logger := gosnmp.NewLogger(log.New(io.Discard, "", 0))
	g := &gosnmp.GoSNMP{
		Version: gosnmp.Version3,
		Logger:  logger,
	}
	if len(cfg.Receiver.V3Users) > 0 {
		table := gosnmp.NewSnmpV3SecurityParametersTable(logger)
		for _, user := range cfg.Receiver.V3Users {
			sec, err := buildUSM(user)
			if err != nil {
				return err
			}
			if err := table.Add(user.UserName, sec); err != nil {
				return err
			}
		}
		g.TrapSecurityParametersTable = table
	}

	d.mu.Lock()
	d.g = g
	d.cfg = cfg
	d.mu.Unlock()
	return nil
}

func (d *GosnmpDecoder) Decode(data []byte, sourceIP string, sourcePort int) (*model.TrapEvent, error) {
	d.mu.RLock()
	g := d.g
	cfg := d.cfg
	d.mu.RUnlock()

	if g != nil {
		packet, err := g.UnmarshalTrap(data, false)
		if err == nil && packet != nil {
			return packetToEvent(packet, data, sourceIP, sourcePort, cfg.FieldAliases), nil
		}
	}

	return snmp.ParseTrap(data, sourceIP, sourcePort, cfg.FieldAliases)
}

func buildUSM(user model.SnmpV3UserConfig) (*gosnmp.UsmSecurityParameters, error) {
	if strings.TrimSpace(user.UserName) == "" {
		return nil, fmt.Errorf("receiver.v3_users[].user_name is required")
	}
	auth, err := parseAuthProtocolStrict(user.AuthenticationProtocol)
	if err != nil {
		return nil, err
	}
	priv, err := parsePrivProtocolStrict(user.PrivacyProtocol)
	if err != nil {
		return nil, err
	}
	sec := &gosnmp.UsmSecurityParameters{
		UserName:                 user.UserName,
		AuthenticationPassphrase: user.AuthenticationPassphrase,
		PrivacyPassphrase:        user.PrivacyPassphrase,
		AuthenticationProtocol:   auth,
		PrivacyProtocol:          priv,
	}
	return sec, nil
}

func parseAuthProtocolStrict(value string) (gosnmp.SnmpV3AuthProtocol, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "noauth":
		return gosnmp.NoAuth, nil
	case "md5":
		return gosnmp.MD5, nil
	case "sha":
		return gosnmp.SHA, nil
	case "sha224":
		return gosnmp.SHA224, nil
	case "sha256":
		return gosnmp.SHA256, nil
	case "sha384":
		return gosnmp.SHA384, nil
	case "sha512":
		return gosnmp.SHA512, nil
	default:
		return gosnmp.NoAuth, fmt.Errorf("unsupported receiver.v3_users[].authentication_protocol %q", value)
	}
}

func parsePrivProtocolStrict(value string) (gosnmp.SnmpV3PrivProtocol, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "nopriv":
		return gosnmp.NoPriv, nil
	case "des":
		return gosnmp.DES, nil
	case "aes":
		return gosnmp.AES, nil
	case "aes192":
		return gosnmp.AES192, nil
	case "aes256":
		return gosnmp.AES256, nil
	case "aes192c":
		return gosnmp.AES192C, nil
	case "aes256c":
		return gosnmp.AES256C, nil
	default:
		return gosnmp.NoPriv, fmt.Errorf("unsupported receiver.v3_users[].privacy_protocol %q", value)
	}
}

func packetToEvent(packet *gosnmp.SnmpPacket, raw []byte, sourceIP string, sourcePort int, fieldAliases map[string]string) *model.TrapEvent {
	event := &model.TrapEvent{
		ReceivedAt:    nowUTC(),
		SourceIP:      sourceIP,
		SourcePort:    sourcePort,
		RawBytes:      append([]byte(nil), raw...),
		Version:       int(packet.Version),
		Community:     packet.Community,
		PDUType:       packet.PDUType.String(),
		RawVarBindMap: map[string]string{},
		Fields:        map[string]string{},
	}

	if packet.Version == gosnmp.Version1 {
		event.EnterpriseOID = packet.Enterprise
		event.AgentAddress = packet.AgentAddress
		event.GenericTrap = fmt.Sprintf("%d", packet.GenericTrap)
		event.GenericTrapName = genericTrapName(packet.GenericTrap)
		event.SpecificTrap = fmt.Sprintf("%d", packet.SpecificTrap)
		event.Uptime = fmt.Sprintf("%d", packet.Timestamp)
		event.TrapOID = fmt.Sprintf("v1:%s:%d:%d", packet.Enterprise, packet.GenericTrap, packet.SpecificTrap)
	}

	for _, pdu := range packet.Variables {
		value := pduValueToString(pdu.Value)
		oidName := oidutil.Normalize(pdu.Name)
		event.VarBinds = append(event.VarBinds, model.VarBind{
			OID:      oidName,
			TypeName: pdu.Type.String(),
			Value:    value,
		})
		event.RawVarBindMap[oidName] = value
	}

	if packet.Version != gosnmp.Version1 {
		event.TrapOID = oidutil.Normalize(firstNonEmpty(
			event.RawVarBindMap["1.3.6.1.6.3.1.1.4.1.0"],
			event.RawVarBindMap[".1.3.6.1.6.3.1.1.4.1.0"],
		))
		event.Uptime = oidutil.Normalize(firstNonEmpty(
			event.RawVarBindMap["1.3.6.1.2.1.1.3.0"],
			event.RawVarBindMap[".1.3.6.1.2.1.1.3.0"],
		))
	}

	event.Fields = buildFields(event, fieldAliases, packet)
	return event
}

func buildFields(event *model.TrapEvent, fieldAliases map[string]string, packet *gosnmp.SnmpPacket) map[string]string {
	fields := map[string]string{
		"source_ip":   event.SourceIP,
		"source_port": fmt.Sprintf("%d", event.SourcePort),
		"version":     fmt.Sprintf("%d", event.Version),
		"community":   event.Community,
		"pdu_type":    event.PDUType,
	}
	if event.EnterpriseOID != "" {
		fields["enterprise_oid"] = event.EnterpriseOID
	}
	if event.AgentAddress != "" {
		fields["agent_address"] = event.AgentAddress
	}
	if event.GenericTrap != "" {
		fields["generic_trap"] = event.GenericTrap
	}
	if event.GenericTrapName != "" {
		fields["generic_trap_name"] = event.GenericTrapName
	}
	if event.SpecificTrap != "" {
		fields["specific_trap"] = event.SpecificTrap
	}
	if event.Uptime != "" {
		fields["uptime"] = event.Uptime
	}
	if event.TrapOID != "" {
		fields["trap_oid"] = oidutil.Normalize(event.TrapOID)
	}
	if packet != nil && packet.Version == gosnmp.Version3 {
		fields["snmp_version"] = "v3"
	}

	for _, vb := range event.VarBinds {
		oidName := oidutil.Normalize(vb.OID)
		fields["varbind."+oidName] = vb.Value
		if alias, ok := lookupAlias(fieldAliases, oidName); ok {
			fields[alias] = vb.Value
			fields["fields."+alias] = vb.Value
		}
	}
	return fields
}

func lookupAlias(fieldAliases map[string]string, oid string) (string, bool) {
	alias, ok := oidutil.Lookup(fieldAliases, oid)
	if !ok || alias == "" {
		return "", false
	}
	return alias, true
}

func pduValueToString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case []byte:
		if utf8.Valid(v) {
			return string(v)
		}
		return "0x" + hex.EncodeToString(v)
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	case net.IP:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func genericTrapName(code int) string {
	switch code {
	case 0:
		return "coldStart"
	case 1:
		return "warmStart"
	case 2:
		return "linkDown"
	case 3:
		return "linkUp"
	case 4:
		return "authenticationFailure"
	case 5:
		return "egpNeighborLoss"
	case 6:
		return "enterpriseSpecific"
	default:
		return fmt.Sprintf("%d", code)
	}
}

func nowUTC() (t time.Time) {
	return time.Now().UTC()
}
