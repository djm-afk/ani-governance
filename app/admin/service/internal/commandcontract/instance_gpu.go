package commandcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	quotapb "go-wind-admin/api/gen/go/quota/service/v1"
	"math"
	"strconv"

	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	protocol "github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/gpuprotocol"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	GpuPhysicalQuotaCode = "gpu.physical.count"
	GpuSharedQuotaCode   = "gpu.shared_memory_mib"
	GpuMeteringVersion   = "gpu-metering-v1"
)

// CanonicalMessage maps the public protobuf to the versioned domain snapshot:
// all fields are present, absent messages are null, enums are decimal integers.
// This deliberately does not hash protojson (enum names/default omission differ).
func CanonicalMessage(m protoreflect.Message) (map[string]any, error) {
	if len(m.GetUnknown()) != 0 {
		return nil, fmt.Errorf("unknown canonical fields")
	}
	out := make(map[string]any)
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		if f.IsMap() {
			values := map[string]any{}
			var mapErr error
			m.Get(f).Map().Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
				values[k.String()], mapErr = canonicalScalar(f.MapValue(), v)
				return mapErr == nil
			})
			if mapErr != nil {
				return nil, mapErr
			}
			out[string(f.Name())] = values
			continue
		}
		var value any
		var err error
		if f.IsList() {
			list := m.Get(f).List()
			values := make([]any, 0, list.Len())
			for j := 0; j < list.Len(); j++ {
				v, e := canonicalScalar(f, list.Get(j))
				if e != nil {
					return nil, e
				}
				values = append(values, v)
			}
			value = values
		} else if f.Kind() == protoreflect.MessageKind && !m.Has(f) {
			value = nil
		} else {
			value, err = canonicalScalar(f, m.Get(f))
		}
		if err != nil {
			return nil, err
		}
		out[string(f.Name())] = value
	}
	return out, nil
}

func canonicalScalar(f protoreflect.FieldDescriptor, v protoreflect.Value) (any, error) {
	switch f.Kind() {
	case protoreflect.MessageKind:
		return CanonicalMessage(v.Message())
	case protoreflect.StringKind:
		return v.String(), nil
	case protoreflect.BoolKind:
		return v.Bool(), nil
	case protoreflect.EnumKind:
		n := int64(v.Enum())
		if n < 0 {
			return nil, fmt.Errorf("negative enum")
		}
		return strconv.FormatInt(n, 10), nil
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind, protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		n := v.Int()
		if n < 0 {
			return nil, fmt.Errorf("negative canonical integer")
		}
		return strconv.FormatInt(n, 10), nil
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		n := v.Uint()
		if n > math.MaxInt64 {
			return nil, fmt.Errorf("canonical integer overflow")
		}
		return strconv.FormatUint(n, 10), nil
	default:
		return nil, fmt.Errorf("unsupported canonical scalar")
	}
}

func CanonicalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}
func Hash(v any) (string, error) {
	b, err := CanonicalJSON(v)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(append([]byte("acc-c14n-v1\n"), b...))
	return hex.EncodeToString(s[:]), nil
}

func SpecDigest(p *acc.GpuProfile) (string, error) {
	if p == nil {
		return "", fmt.Errorf("invalid spec")
	}
	return protocol.ProfileSpecDigest(p.Spec, p.BaselineId)
}

func GpuResolvedQuota(p *acc.ResolvedGpuRequest, request *acc.GpuRequest) (QuotaItem, error) {
	if err := protocol.ValidateResolvedGpuRequest(request, p); err != nil {
		return QuotaItem{}, quotapb.ErrorInvalidQuotaRequest("%s", "invalid resolved GPU execution").WithCause(err)
	}
	return QuotaItem{QuotaCode: p.QuotaItems[0].QuotaCode, Units: p.QuotaItems[0].Units}, nil
}

type GpuQuotaItem struct {
	QuotaCode string `json:"quota_code"`
	Units     int64  `json:"units"`
}
type GpuCanonical struct {
	SchemaVersion         uint32          `json:"schema_version"`
	GpuRequest            *acc.GpuRequest `json:"gpu_request"`
	BusinessPayload       json.RawMessage `json:"business_payload"`
	BusinessPayloadDigest string          `json:"business_payload_digest"`
	MeteringVersion       string          `json:"metering_version"`
	QuotaItems            []GpuQuotaItem  `json:"quota_items"`
	DeleteExpectedVersion int64           `json:"delete_expected_version,omitempty"`
}

func IsGpuCanonical(raw []byte) bool {
	var marker struct {
		SchemaVersion uint32 `json:"schema_version"`
	}
	return json.Unmarshal(raw, &marker) == nil && marker.SchemaVersion == 3
}
func DecodeGpuCanonical(raw []byte) (*GpuCanonical, error) {
	var c GpuCanonical
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || c.SchemaVersion != 3 || c.DeleteExpectedVersion < 0 || c.MeteringVersion != GpuMeteringVersion || c.GpuRequest == nil || len(c.QuotaItems) == 0 {
		return nil, quotapb.ErrorInvalidQuotaRequest("%s", "invalid GPU canonical")
	}
	encoded, err := CanonicalJSON(c)
	if err != nil || !bytes.Equal(encoded, raw) {
		return nil, quotapb.ErrorInvalidQuotaRequest("%s", "noncanonical GPU command")
	}
	var gpuItems []GpuQuotaItem
	seen := map[string]bool{}
	for _, q := range c.QuotaItems {
		if q.QuotaCode == "" || q.Units <= 0 || seen[q.QuotaCode] {
			return nil, quotapb.ErrorInvalidQuotaRequest("%s", "invalid frozen quota vector")
		}
		seen[q.QuotaCode] = true
		if q.QuotaCode == GpuSharedQuotaCode || q.QuotaCode == GpuPhysicalQuotaCode {
			gpuItems = append(gpuItems, q)
		}
	}
	if len(gpuItems) != 1 {
		return nil, quotapb.ErrorInvalidQuotaRequest("%s", "invalid GPU metering")
	}
	q := gpuItems[0]
	business, err := res.DecodeBusinessPayload(c.BusinessPayload)
	if err != nil {
		return nil, err
	}
	_, err = GpuResolvedQuota(&acc.ResolvedGpuRequest{Execution: business.GpuExecution, BusinessSpec: business.GpuBusinessSpec, QuotaItems: []*acc.GpuQuotaItem{{QuotaCode: q.QuotaCode, Units: q.Units}}}, c.GpuRequest)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(c.BusinessPayload)
	if hex.EncodeToString(sum[:]) != c.BusinessPayloadDigest {
		return nil, quotapb.ErrorInvalidQuotaRequest("%s", "business payload digest mismatch")
	}
	return &c, nil
}
func ValidateGpuCharges(c *GpuCanonical, charges []ChargeRef) error {
	if c == nil || len(charges) != len(c.QuotaItems) {
		return quotapb.ErrorInvalidQuotaRequest("%s", "incomplete original charges")
	}
	want := map[string]int64{}
	for _, q := range c.QuotaItems {
		want[q.QuotaCode] = q.Units
	}
	seenCode, seenID := map[string]bool{}, map[string]bool{}
	for _, q := range charges {
		if q.ChargeID == "" || seenID[q.ChargeID] || seenCode[q.QuotaCode] || q.ChargedUnits <= 0 || want[q.QuotaCode] != q.ChargedUnits {
			return quotapb.ErrorInvalidQuotaRequest("%s", "original charges mismatch")
		}
		seenID[q.ChargeID] = true
		seenCode[q.QuotaCode] = true
	}
	return nil
}
