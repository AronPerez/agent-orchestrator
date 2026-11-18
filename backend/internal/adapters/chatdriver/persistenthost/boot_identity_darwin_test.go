//go:build darwin

package persistenthost

import "testing"

func TestDarwinBootIdentityStableAcrossClockAdjustment(t *testing.T) {
	const bootSessionUUID = "A86E366B-4BE1-4AA7-B8AC-C3E4014C98E1"
	wallClockBootTime := "1710000000.000001"
	var requested []string
	readSysctl := func(name string) (string, error) {
		requested = append(requested, name)
		switch name {
		case "kern.bootsessionuuid":
			return bootSessionUUID, nil
		case "kern.boottime":
			return wallClockBootTime, nil
		default:
			t.Fatalf("unexpected sysctl %q", name)
			return "", nil
		}
	}

	before, err := darwinBootIdentity(readSysctl)
	if err != nil {
		t.Fatal(err)
	}
	wallClockBootTime = "1710003600.000001"
	after, err := darwinBootIdentity(readSysctl)
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("boot identity changed with wall clock: %q -> %q", before, after)
	}
	if before != "darwin:a86e366b-4be1-4aa7-b8ac-c3e4014c98e1" {
		t.Fatalf("boot identity = %q", before)
	}
	for _, name := range requested {
		if name != "kern.bootsessionuuid" {
			t.Fatalf("boot identity read mutable sysctl %q", name)
		}
	}
}
