package v3authorization

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

type managementWire struct {
	EventID          string            `json:"event_id"`
	CorrelationID    string            `json:"correlation_id"`
	ExpectedRevision uint64            `json:"expected_revision"`
	Scope            managementScope   `json:"scope"`
	Action           string            `json:"action"`
	Records          []json.RawMessage `json:"records,omitempty"`
}
type managementScope struct {
	Realm    string `json:"realm"`
	Host     string `json:"host"`
	Resource string `json:"resource"`
	Backend  string `json:"backend"`
}

func managementDecode(data []byte, target any) error {
	if len(data) == 0 || len(data) > 64<<10 || !utf8.Valid(data) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if managementValue(d, reflect.TypeOf(target).Elem(), 0) != nil {
		return ErrInvalid
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrInvalid
	}
	if json.Unmarshal(data, target) != nil {
		return ErrInvalid
	}
	return nil
}
func managementValue(d *json.Decoder, t reflect.Type, depth int) error {
	if depth > 16 {
		return ErrInvalid
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	tok, e := d.Token()
	if e != nil || tok == nil {
		return ErrInvalid
	}
	if t == rawType {
		return strictRaw(d, tok, depth)
	}
	switch t.Kind() {
	case reflect.Struct:
		if tok != json.Delim('{') {
			return ErrInvalid
		}
		fields := map[string]reflect.StructField{}
		required := map[string]bool{}
		for n := 0; n < t.NumField(); n++ {
			f := t.Field(n)
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "" {
				name = f.Name
			}
			fields[name] = f
			required[name] = !strings.Contains(tag, ",omitempty")
		}
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			name, ok := k.(string)
			f, exists := fields[name]
			if e != nil || !ok || !exists || seen[name] {
				return ErrInvalid
			}
			seen[name] = true
			if managementValue(d, f.Type, depth+1) != nil {
				return ErrInvalid
			}
		}
		if end, e := d.Token(); e != nil || end != json.Delim('}') {
			return ErrInvalid
		}
		for k, must := range required {
			if must && !seen[k] {
				return ErrInvalid
			}
		}
	case reflect.Slice:
		if tok != json.Delim('[') {
			return ErrInvalid
		}
		n := 0
		for d.More() {
			n++
			if n > 32 || managementValue(d, t.Elem(), depth+1) != nil {
				return ErrInvalid
			}
		}
		if end, e := d.Token(); e != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	case reflect.String:
		s, ok := tok.(string)
		if !ok || strings.ContainsRune(s, utf8.RuneError) {
			return ErrInvalid
		}
	case reflect.Bool:
		if _, ok := tok.(bool); !ok {
			return ErrInvalid
		}
	case reflect.Uint64:
		n, ok := tok.(json.Number)
		if !ok {
			return ErrInvalid
		}
		s := string(n)
		for _, c := range s {
			if c < '0' || c > '9' {
				return ErrInvalid
			}
		}
		if _, e := strconv.ParseUint(s, 10, 64); e != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func managementCarrier(data []byte) (ManagementMetadata, []json.RawMessage, error) {
	var w managementWire
	if managementDecode(data, &w) != nil {
		return ManagementMetadata{}, nil, ErrInvalid
	}
	m := ManagementMetadata{EventID: w.EventID, CorrelationID: w.CorrelationID, ExpectedRevision: w.ExpectedRevision, Action: w.Action}
	m.Scope.Realm = w.Scope.Realm
	m.Scope.Host = w.Scope.Host
	m.Scope.Resource = w.Scope.Resource
	m.Scope.Backend = w.Scope.Backend
	if !validManagementMetadata(m) {
		return ManagementMetadata{}, nil, ErrInvalid
	}
	return m, w.Records, nil
}
func DecodeManagementRecords(data []byte, kind AuthorityRecordKind) (ManagementMetadata, []AuthorityRecord, error) {
	m, raw, err := managementCarrier(data)
	if err != nil || len(raw) == 0 || managementOperation(kind) == "" {
		return ManagementMetadata{}, nil, ErrInvalid
	}
	out := make([]AuthorityRecord, 0, len(raw))
	for _, body := range raw {
		r := AuthorityRecord{Kind: kind}
		var target any
		switch kind {
		case RealmRecord:
			r.Realm = &RealmRegistration{}
			target = r.Realm
		case HostRecord:
			r.Host = &HostRegistration{}
			target = r.Host
		case ResourceRecord:
			r.Resource = &ResourceRegistration{}
			target = r.Resource
		case BackendRecord:
			r.Backend = &BackendRegistration{}
			target = r.Backend
		case CandidateRecord:
			r.Candidate = &CandidateApproval{}
			target = r.Candidate
		case CapabilityRecord:
			r.Capability = &V3Capability{}
			target = r.Capability
		case MembershipRecord:
			r.Membership = &DomainMembership{}
			target = r.Membership
		case PolicyRecord, TrustRecord:
			r.Rule = &ScopeRule{}
			target = r.Rule
		}
		if managementDecode(body, target) != nil {
			return ManagementMetadata{}, nil, ErrInvalid
		}
		if _, _, err := encodeAuthorityRecord(r); err != nil {
			return ManagementMetadata{}, nil, ErrInvalid
		}
		out = append(out, r)
	}
	return m, out, nil
}
func DecodeManagementAdministrators(data []byte) (ManagementMetadata, []ManagementGrant, error) {
	m, raw, err := managementCarrier(data)
	if err != nil || len(raw) == 0 {
		return ManagementMetadata{}, nil, ErrInvalid
	}
	out := make([]ManagementGrant, 0, len(raw))
	for _, body := range raw {
		var g ManagementGrant
		if managementDecode(body, &g) != nil {
			return ManagementMetadata{}, nil, ErrInvalid
		}
		if _, err := encodeManagementGrant(g); err != nil {
			return ManagementMetadata{}, nil, ErrInvalid
		}
		out = append(out, g)
	}
	return m, out, nil
}
func DecodeManagementInspection(data []byte) (ManagementMetadata, error) {
	m, raw, e := managementCarrier(data)
	if e != nil || len(raw) != 0 {
		return ManagementMetadata{}, ErrInvalid
	}
	return m, nil
}
