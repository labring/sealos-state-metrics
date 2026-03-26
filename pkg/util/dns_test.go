//nolint:testpackage // Tests need access to internal DNS family filtering helper.
package util

import (
	"reflect"
	"testing"
)

func TestFilterIPsByFamily(t *testing.T) {
	ips := []string{
		"104.18.26.120",
		"2606:4700::6812:1a78",
		"104.18.27.120",
		"2606:4700::6812:1b78",
	}

	tests := []struct {
		name   string
		filter IPFamilyFilter
		want   []string
	}{
		{
			name: "all families",
			filter: IPFamilyFilter{
				IncludeIPv4: true,
				IncludeIPv6: true,
			},
			want: ips,
		},
		{
			name: "ipv4 only",
			filter: IPFamilyFilter{
				IncludeIPv4: true,
				IncludeIPv6: false,
			},
			want: []string{"104.18.26.120", "104.18.27.120"},
		},
		{
			name: "ipv6 only",
			filter: IPFamilyFilter{
				IncludeIPv4: false,
				IncludeIPv6: true,
			},
			want: []string{"2606:4700::6812:1a78", "2606:4700::6812:1b78"},
		},
		{
			name: "disabled all",
			filter: IPFamilyFilter{
				IncludeIPv4: false,
				IncludeIPv6: false,
			},
			want: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterIPsByFamily(ips, tt.filter)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("filterIPsByFamily() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
