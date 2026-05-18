package cockroachlicense

import (
	"encoding/base64"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
)

const licensePrefix = "crl-0-"

type licenseType int32

const (
	licenseTypeNonCommercial licenseType = 0
	licenseTypeEnterprise    licenseType = 1
	licenseTypeEvaluation    licenseType = 2
	licenseTypeFree          licenseType = 3
	licenseTypeTrial         licenseType = 4
)

type licenseEnvironment int32

const (
	licenseEnvironmentUnspecified   licenseEnvironment = 0
	licenseEnvironmentProduction    licenseEnvironment = 1
	licenseEnvironmentPreProduction licenseEnvironment = 2
	licenseEnvironmentDevelopment   licenseEnvironment = 3
)

type licenseInfo struct {
	validUntilUnixSec int64
	licenseType       licenseType
	organizationName  string
	environment       licenseEnvironment
	licenseID         string
	organizationID    string
}

func decodeLicense(s string) (*licenseInfo, error) {
	if s == "" {
		return nil, nil
	}
	if !strings.HasPrefix(s, licensePrefix) {
		return nil, fmt.Errorf("invalid license string")
	}

	payload := strings.TrimPrefix(s, licensePrefix)
	data, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid license string: %w", err)
	}

	info := &licenseInfo{}
	for len(data) > 0 {
		num, typ, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, fmt.Errorf("invalid license protobuf tag")
		}
		data = data[n:]

		switch num {
		case 2:
			if typ != protowire.VarintType {
				return nil, fmt.Errorf("invalid license valid_until field type")
			}
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("invalid license valid_until field")
			}
			info.validUntilUnixSec = int64(v)
			data = data[n:]
		case 3:
			if typ != protowire.VarintType {
				return nil, fmt.Errorf("invalid license type field type")
			}
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("invalid license type field")
			}
			info.licenseType = licenseType(v)
			data = data[n:]
		case 4:
			if typ != protowire.BytesType {
				return nil, fmt.Errorf("invalid license organization_name field type")
			}
			v, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return nil, fmt.Errorf("invalid license organization_name field")
			}
			info.organizationName = string(v)
			data = data[n:]
		case 5:
			if typ != protowire.VarintType {
				return nil, fmt.Errorf("invalid license environment field type")
			}
			v, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil, fmt.Errorf("invalid license environment field")
			}
			info.environment = licenseEnvironment(v)
			data = data[n:]
		case 6:
			v, n, err := consumeUUIDBytes(data, typ, "license_id")
			if err != nil {
				return nil, err
			}
			info.licenseID = v
			data = data[n:]
		case 7:
			v, n, err := consumeUUIDBytes(data, typ, "organization_id")
			if err != nil {
				return nil, err
			}
			info.organizationID = v
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(num, typ, data)
			if n < 0 {
				return nil, fmt.Errorf("invalid unknown license field %d", num)
			}
			data = data[n:]
		}
	}

	return info, nil
}

func consumeUUIDBytes(data []byte, typ protowire.Type, field string) (string, int, error) {
	if typ != protowire.BytesType {
		return "", 0, fmt.Errorf("invalid license %s field type", field)
	}
	v, n := protowire.ConsumeBytes(data)
	if n < 0 {
		return "", 0, fmt.Errorf("invalid license %s field", field)
	}
	if len(v) == 0 {
		return "", n, nil
	}
	if len(v) != 16 {
		return "", n, fmt.Errorf("invalid license %s length", field)
	}
	return formatUUID(v), n, nil
}

func formatUUID(b []byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func licenseTypeString(t licenseType) string {
	switch t {
	case licenseTypeNonCommercial:
		return "NonCommercial"
	case licenseTypeEnterprise:
		return "Enterprise"
	case licenseTypeEvaluation:
		return "Evaluation"
	case licenseTypeFree:
		return "Free"
	case licenseTypeTrial:
		return "Trial"
	default:
		return "Unknown"
	}
}

func licenseTypeValue(t licenseType) float64 {
	switch t {
	case licenseTypeEnterprise:
		return 1
	case licenseTypeEvaluation:
		return 2
	case licenseTypeFree:
		return 3
	case licenseTypeTrial:
		return 4
	case licenseTypeNonCommercial:
		return 5
	default:
		return 0
	}
}

func environmentString(e licenseEnvironment) string {
	switch e {
	case licenseEnvironmentProduction:
		return "production"
	case licenseEnvironmentPreProduction:
		return "pre-production"
	case licenseEnvironmentDevelopment:
		return "development"
	case licenseEnvironmentUnspecified:
		return ""
	default:
		return "other"
	}
}
