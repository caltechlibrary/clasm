package awsclient

import "testing"

func TestRegions(t *testing.T) {
	want := []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2"}
	if len(Regions) != len(want) {
		t.Fatalf("len(Regions) = %d, want %d", len(Regions), len(want))
	}
	for i, region := range want {
		if Regions[i] != region {
			t.Errorf("Regions[%d] = %q, want %q", i, Regions[i], region)
		}
	}
}
