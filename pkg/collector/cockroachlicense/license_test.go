//nolint:testpackage // Tests exercise private protobuf parser details.
package cockroachlicense

import (
	"encoding/base64"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestDecodeLicense(t *testing.T) {
	lic := encodeTestLicense()

	info, err := decodeLicense(lic)
	if err != nil {
		t.Fatal(err)
	}

	if info == nil {
		t.Fatal("expected license info")
	}

	if got, want := licenseTypeString(info.licenseType), "Trial"; got != want {
		t.Fatalf("expected type %s, got %s", want, got)
	}

	if got, want := info.validUntilUnixSec, int64(1781428138); got != want {
		t.Fatalf("expected expiry %d, got %d", want, got)
	}

	if got, want := info.licenseID, "00010203-0405-0607-0809-0a0b0c0d0e0f"; got != want {
		t.Fatalf("expected license id %s, got %s", want, got)
	}

	if got, want := info.organizationID, "10111213-1415-1617-1819-1a1b1c1d1e1f"; got != want {
		t.Fatalf("expected organization id %s, got %s", want, got)
	}
}

func TestDecodeLicenseRejectsOverflowingVarints(t *testing.T) {
	tests := []struct {
		name       string
		field      protowire.Number
		value      uint64
		wantErrSub string
	}{
		{
			name:       "valid until",
			field:      2,
			value:      uint64(math.MaxInt64) + 1,
			wantErrSub: "valid_until field overflow",
		},
		{
			name:       "license type",
			field:      3,
			value:      uint64(math.MaxInt32) + 1,
			wantErrSub: "type field overflow",
		},
		{
			name:       "environment",
			field:      5,
			value:      uint64(math.MaxInt32) + 1,
			wantErrSub: "environment field overflow",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var data []byte

			data = protowire.AppendTag(data, tt.field, protowire.VarintType)
			data = protowire.AppendVarint(data, tt.value)

			_, err := decodeLicense(licensePrefix + base64.RawStdEncoding.EncodeToString(data))
			if err == nil {
				t.Fatal("expected error")
			}

			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %q", tt.wantErrSub, err.Error())
			}
		})
	}
}

func encodeTestLicense() string {
	licenseID := []byte{
		0x00, 0x01, 0x02, 0x03,
		0x04, 0x05,
		0x06, 0x07,
		0x08, 0x09,
		0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	}
	organizationID := []byte{
		0x10, 0x11, 0x12, 0x13,
		0x14, 0x15,
		0x16, 0x17,
		0x18, 0x19,
		0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
	}

	var data []byte

	data = protowire.AppendTag(data, 2, protowire.VarintType)
	data = protowire.AppendVarint(data, 1781428138)
	data = protowire.AppendTag(data, 3, protowire.VarintType)
	data = protowire.AppendVarint(data, uint64(licenseTypeTrial))
	data = protowire.AppendTag(data, 4, protowire.BytesType)
	data = protowire.AppendBytes(data, []byte("unit-test"))
	data = protowire.AppendTag(data, 5, protowire.VarintType)
	data = protowire.AppendVarint(data, uint64(licenseEnvironmentDevelopment))
	data = protowire.AppendTag(data, 6, protowire.BytesType)
	data = protowire.AppendBytes(data, licenseID)
	data = protowire.AppendTag(data, 7, protowire.BytesType)
	data = protowire.AppendBytes(data, organizationID)

	return licensePrefix + base64.RawStdEncoding.EncodeToString(data)
}
