package lvm_test

import (
	"testing"

	"github.com/labring/sealos-state-metrics/pkg/lvm"
)

func TestListLVMVolumeGroup(t *testing.T) {
	vgs, err := lvm.ListLVMVolumeGroup(false)
	if err != nil {
		t.Fatalf("Failed to list LVM volume group: %v", err)
	}

	if len(vgs) == 0 {
		t.Fatal("Failed to list LVM volume group: vg not found")
	}

	for i := range vgs {
		t.Logf("Found LVM volume group: %#+v", vgs[i])
	}
}
