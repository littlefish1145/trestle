package vcpkg

import "testing"

func TestRankTriplets(t *testing.T) {
	candidates, err := RankTriplets(TripletRequest{OS: "windows", Arch: "amd64", Available: []string{"x64-windows", "arm64-windows", "x64-linux"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Name != "x64-windows" {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
}

func TestSelectAutoRejectsAmbiguous(t *testing.T) {
	_, err := SelectAutoTriplet(TripletRequest{OS: "linux", Arch: "amd64", Available: []string{"x64-linux", "x64-linux-static"}})
	if err == nil {
		t.Fatal("expected ambiguous triplet error")
	}
}
