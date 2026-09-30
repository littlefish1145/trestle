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

func TestTripletsMatchTargetArchitectureAndABI(t *testing.T) {
	if got := TripletFor("darwin", "arm64"); got != "arm64-osx" {
		t.Fatal(got)
	}
	if got := TripletFor("linux", "386"); got != "x86-linux" {
		t.Fatal(got)
	}
	candidates, err := RankTriplets(TripletRequest{OS: "windows", Arch: "amd64", Compiler: "gcc", Available: []string{"x64-windows", "arm64-mingw-dynamic", "x64-mingw-dynamic"}})
	if err != nil || len(candidates) != 1 || candidates[0].Name != "x64-mingw-dynamic" {
		t.Fatalf("wrong ABI: %+v %v", candidates, err)
	}
}
