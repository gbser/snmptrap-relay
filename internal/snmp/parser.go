package snmp

import (
	"fmt"
	"strconv"
	"time"

	"snmptrap-relay/internal/ber"
	"snmptrap-relay/internal/model"
	"snmptrap-relay/internal/oidutil"
)

const (
	snmpTrapOID  = "1.3.6.1.6.3.1.1.4.1.0"
	sysUpTimeOID = "1.3.6.1.2.1.1.3.0"
)

var genericTrapNames = map[int64]string{
	0: "coldStart",
	1: "warmStart",
	2: "linkDown",
	3: "linkUp",
	4: "authenticationFailure",
	5: "egpNeighborLoss",
	6: "enterpriseSpecific",
}

type ParseError struct {
	Msg string
}

func (e *ParseError) Error() string { return e.Msg }

func ParseTrap(data []byte, sourceIP string, sourcePort int, fieldAliases map[string]string) (*model.TrapEvent, error) {
	reader := ber.NewReader(data)
	outer, err := reader.ReadTLV()
	if err != nil {
		return nil, err
	}
	if outer.Tag != 0x30 {
		return nil, &ParseError{Msg: "SNMP message must be a SEQUENCE"}
	}

	msgReader := ber.NewReader(outer.Value)
	versionTLV, err := msgReader.ReadTLV()
	if err != nil {
		return nil, err
	}
	if ber.DecodeTagClass(versionTLV.Tag) != "universal" || ber.DecodeTagNumber(versionTLV.Tag) != 0x02 {
		return nil, &ParseError{Msg: "missing SNMP version"}
	}
	version, err := strconv.ParseInt(ber.DecodeSignedBigInt(versionTLV.Value).String(), 10, 64)
	if err != nil {
		return nil, err
	}

	communityTLV, err := msgReader.ReadTLV()
	if err != nil {
		return nil, err
	}
	if ber.DecodeTagClass(communityTLV.Tag) != "universal" || ber.DecodeTagNumber(communityTLV.Tag) != 0x04 {
		return nil, &ParseError{Msg: "missing SNMP community"}
	}

	pduTLV, err := msgReader.ReadTLV()
	if err != nil {
		return nil, err
	}

	event := &model.TrapEvent{
		ReceivedAt:    time.Now().UTC(),
		SourceIP:      sourceIP,
		SourcePort:    sourcePort,
		RawBytes:      append([]byte(nil), data...),
		Version:       int(version),
		Community:     string(communityTLV.Value),
		PDUType:       fmt.Sprintf("0x%02x", pduTLV.Tag),
		RawVarBindMap: map[string]string{},
		Fields:        map[string]string{},
	}

	switch pduTLV.Tag {
	case 0xA4:
		if err := parseV1(event, pduTLV.Value); err != nil {
			return nil, err
		}
	case 0xA7:
		if err := parseV2(event, pduTLV.Value); err != nil {
			return nil, err
		}
	default:
		return nil, &ParseError{Msg: fmt.Sprintf("unsupported PDU tag 0x%02x", pduTLV.Tag)}
	}

	buildFields(event, fieldAliases)
	return event, nil
}

func parseV1(event *model.TrapEvent, body []byte) error {
	reader := ber.NewReader(body)

	enterpriseTLV, err := reader.ReadTLV()
	if err != nil {
		return err
	}
	enterpriseOID, err := ber.DecodeOID(enterpriseTLV.Value)
	if err != nil {
		return err
	}

	agentTLV, err := reader.ReadTLV()
	if err != nil {
		return err
	}
	_, agentAddrValue, err := ber.DecodeValue(agentTLV)
	if err != nil {
		return err
	}

	genericTLV, err := reader.ReadTLV()
	if err != nil {
		return err
	}
	specificTLV, err := reader.ReadTLV()
	if err != nil {
		return err
	}
	uptimeTLV, err := reader.ReadTLV()
	if err != nil {
		return err
	}

	generic := ber.DecodeSignedBigInt(genericTLV.Value).Int64()
	specific := ber.DecodeSignedBigInt(specificTLV.Value).Int64()

	event.EnterpriseOID = enterpriseOID
	event.AgentAddress = agentAddrValue
	event.GenericTrap = strconv.FormatInt(generic, 10)
	event.GenericTrapName = genericTrapNames[generic]
	if event.GenericTrapName == "" {
		event.GenericTrapName = event.GenericTrap
	}
	event.SpecificTrap = strconv.FormatInt(specific, 10)
	event.Uptime = ber.DecodeSignedBigInt(uptimeTLV.Value).String()

	varbinds, err := decodeVarBinds(reader)
	if err != nil {
		return err
	}
	event.VarBinds = varbinds
	for _, vb := range varbinds {
		event.RawVarBindMap[vb.OID] = vb.Value
	}

	event.TrapOID = fmt.Sprintf("v1:%s:%d:%d", event.EnterpriseOID, generic, specific)
	return nil
}

func parseV2(event *model.TrapEvent, body []byte) error {
	reader := ber.NewReader(body)

	requestTLV, err := reader.ReadTLV()
	if err != nil {
		return err
	}
	errorStatusTLV, err := reader.ReadTLV()
	if err != nil {
		return err
	}
	errorIndexTLV, err := reader.ReadTLV()
	if err != nil {
		return err
	}

	event.RequestID, _ = strconv.ParseInt(ber.DecodeSignedBigInt(requestTLV.Value).String(), 10, 64)
	event.ErrorStatus, _ = strconv.ParseInt(ber.DecodeSignedBigInt(errorStatusTLV.Value).String(), 10, 64)
	event.ErrorIndex, _ = strconv.ParseInt(ber.DecodeSignedBigInt(errorIndexTLV.Value).String(), 10, 64)

	varbinds, err := decodeVarBinds(reader)
	if err != nil {
		return err
	}
	event.VarBinds = varbinds
	for _, vb := range varbinds {
		event.RawVarBindMap[vb.OID] = vb.Value
	}
	event.TrapOID = oidutil.Normalize(firstNonEmpty(
		event.RawVarBindMap[snmpTrapOID],
		event.RawVarBindMap["."+snmpTrapOID],
	))
	event.Uptime = oidutil.Normalize(firstNonEmpty(
		event.RawVarBindMap[sysUpTimeOID],
		event.RawVarBindMap["."+sysUpTimeOID],
	))
	return nil
}

func decodeVarBinds(reader *ber.Reader) ([]model.VarBind, error) {
	sequence, err := reader.ReadTLV()
	if err != nil {
		return nil, err
	}
	if sequence.Tag != 0x30 {
		return nil, &ParseError{Msg: "expected varbind sequence"}
	}

	listReader := ber.NewReader(sequence.Value)
	var result []model.VarBind
	for !listReader.EOF() {
		item, err := listReader.ReadTLV()
		if err != nil {
			return nil, err
		}
		if item.Tag != 0x30 {
			return nil, &ParseError{Msg: "expected varbind item sequence"}
		}
		itemReader := ber.NewReader(item.Value)
		oidTLV, err := itemReader.ReadTLV()
		if err != nil {
			return nil, err
		}
		if ber.DecodeTagClass(oidTLV.Tag) != "universal" || ber.DecodeTagNumber(oidTLV.Tag) != 0x06 {
			return nil, &ParseError{Msg: "expected OID in varbind"}
		}
		oid, err := ber.DecodeOID(oidTLV.Value)
		if err != nil {
			return nil, err
		}
		valueTLV, err := itemReader.ReadTLV()
		if err != nil {
			return nil, err
		}
		typeName, value, err := ber.DecodeValue(valueTLV)
		if err != nil {
			return nil, err
		}
		result = append(result, model.VarBind{OID: oidutil.Normalize(oid), TypeName: typeName, Value: value})
	}
	return result, nil
}

func buildFields(event *model.TrapEvent, fieldAliases map[string]string) {
	fields := map[string]string{
		"source_ip":   event.SourceIP,
		"source_port": strconv.Itoa(event.SourcePort),
		"version":     strconv.Itoa(event.Version),
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
	if event.RequestID != 0 {
		fields["request_id"] = strconv.FormatInt(event.RequestID, 10)
	}
	if event.ErrorStatus != 0 {
		fields["error_status"] = strconv.FormatInt(event.ErrorStatus, 10)
	}
	if event.ErrorIndex != 0 {
		fields["error_index"] = strconv.FormatInt(event.ErrorIndex, 10)
	}
	if event.TrapOID != "" {
		fields["trap_oid"] = oidutil.Normalize(event.TrapOID)
	}

	for _, vb := range event.VarBinds {
		oidName := oidutil.Normalize(vb.OID)
		fields["varbind."+oidName] = vb.Value
		if alias, ok := lookupAlias(fieldAliases, oidName); ok {
			fields[alias] = vb.Value
			fields["fields."+alias] = vb.Value
		}
	}

	event.Fields = fields
}

func lookupAlias(fieldAliases map[string]string, oid string) (string, bool) {
	alias, ok := oidutil.Lookup(fieldAliases, oid)
	if !ok || alias == "" {
		return "", false
	}
	return alias, true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
