package video

import "testing"

func TestArkSizeMapsToNearestSupportedRatio(t *testing.T) {
	ratio, resolution, err := arkSize("13:7")
	if err != nil {
		t.Fatal(err)
	}
	if ratio != "16:9" || resolution != "" {
		t.Fatalf("arkSize(13:7) = %q, %q", ratio, resolution)
	}
	ratio, resolution, err = arkSize("1280x720")
	if err != nil || ratio != "16:9" || resolution != "720p" {
		t.Fatalf("arkSize(1280x720) = %q, %q, %v", ratio, resolution, err)
	}
}
