package toolchain

import "testing"

func TestParseRangeAcceptedSpellings(t *testing.T) {
	cases := []struct {
		spec string
		want Range
	}{
		{"", Range{}},
		{"auto", Range{}},
		{"any", Range{}},
		{"12.4", Range{Min: "12.4", Max: "12.4"}},
		{"12.0~12.9", Range{Min: "12.0", Max: "12.9"}},
		{"~14.5", Range{Max: "14.5"}},
		{"14.3~", Range{Min: "14.3"}},
		{" 12.0 ~ 12.9 ", Range{Min: "12.0", Max: "12.9"}},
	}
	for _, testCase := range cases {
		got, err := ParseRange(testCase.spec)
		if err != nil {
			t.Fatalf("ParseRange(%q): %v", testCase.spec, err)
		}
		if got != testCase.want {
			t.Fatalf("ParseRange(%q) = %#v, want %#v", testCase.spec, got, testCase.want)
		}
	}
}

func TestParseRangeRejectsMalformedVersions(t *testing.T) {
	for _, spec := range []string{"12.x", "12..4", "abc", "12.4~abc", "1 2"} {
		if _, err := ParseRange(spec); err == nil {
			t.Fatalf("ParseRange(%q) accepted a malformed version", spec)
		}
	}
	// Open bounds are valid on either side.
	for _, spec := range []string{"12.4~", "~", "~14.5"} {
		if _, err := ParseRange(spec); err != nil {
			t.Fatalf("ParseRange(%q) rejected an open bound: %v", spec, err)
		}
	}
}

func TestRangeContains(t *testing.T) {
	interval, err := ParseRange("14.3~14.5")
	if err != nil {
		t.Fatal(err)
	}
	for _, inside := range []string{"14.3", "14.5", "14.4"} {
		if !interval.Contains(inside) {
			t.Fatalf("%q should satisfy 14.3~14.5", inside)
		}
	}
	for _, outside := range []string{"14.2", "14.6", "15.0", "not-a-version", ""} {
		if interval.Contains(outside) {
			t.Fatalf("%q should not satisfy 14.3~14.5", outside)
		}
	}
	unbounded, err := ParseRange("")
	if err != nil {
		t.Fatal(err)
	}
	if !unbounded.Any() || !unbounded.Contains("anything") {
		t.Fatalf("an empty range must accept everything: %#v", unbounded)
	}
	openEnded, err := ParseRange("12.0~")
	if err != nil {
		t.Fatal(err)
	}
	if !openEnded.Contains("99.0") || openEnded.Contains("11.9") {
		t.Fatalf("12.0~ must be unbounded above")
	}
}

// Bounds compare numerically over the components they spell out, which is how a
// two-component range covers a three-component toolset directory.
func TestRangeComparesOnlySpelledComponents(t *testing.T) {
	toolsetSeries, err := ParseRange("14.30~14.50")
	if err != nil {
		t.Fatal(err)
	}
	if !toolsetSeries.Contains("14.44.35207") {
		t.Fatal("14.30~14.50 must accept the 14.44.35207 toolset")
	}
	for _, outside := range []string{"14.29.99999", "14.51", "15.0"} {
		if toolsetSeries.Contains(outside) {
			t.Fatalf("%q must fall outside 14.30~14.50", outside)
		}
	}
	// A narrower minor really is narrower: 14.3 means the literal minor 3.
	narrow, err := ParseRange("14.3~14.4")
	if err != nil {
		t.Fatal(err)
	}
	if narrow.Contains("14.44.35207") || !narrow.Contains("14.3") {
		t.Fatal("14.3~14.4 must only cover minors 3 and 4")
	}
	cudaSeries, err := ParseRange("12.0~12.9")
	if err != nil {
		t.Fatal(err)
	}
	for _, inside := range []string{"12.0", "12.4", "12.9.131", "12.9"} {
		if !cudaSeries.Contains(inside) {
			t.Fatalf("%q should satisfy 12.0~12.9", inside)
		}
	}
	if cudaSeries.Contains("12.10") {
		t.Fatal("12.0~12.9 must reject CUDA 12.10")
	}
	pinned, err := ParseRange("14.44.35207")
	if err != nil {
		t.Fatal(err)
	}
	if !pinned.Contains("14.44.35207") || pinned.Contains("14.44.35208") {
		t.Fatal("a full-precision pin must compare every component")
	}
}

func TestCompareVersionsTreatsMissingComponentsAsZero(t *testing.T) {
	if CompareVersions("14.44", "14.44.0") != 0 {
		t.Fatal("14.44 and 14.44.0 must compare equal")
	}
	if CompareVersions("14.9", "14.10") >= 0 {
		t.Fatal("components must compare numerically, not lexically")
	}
	if CompareVersions("12.4.1", "12.4") <= 0 {
		t.Fatal("12.4.1 must be newer than 12.4")
	}
}

func TestNormalizeVersionStripsLabels(t *testing.T) {
	for input, want := range map[string]string{
		"v12.4":        "12.4",
		"12.4.131":     "12.4.131",
		"12.4-rc1":     "12.4",
		"V14.44.35207": "14.44.35207",
	} {
		got, ok := NormalizeVersion(input)
		if !ok || got != want {
			t.Fatalf("NormalizeVersion(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	if _, ok := NormalizeVersion("cuda"); ok {
		t.Fatal("a name is not a version")
	}
}

func TestExtractVersionFromToolOutput(t *testing.T) {
	cases := map[string]string{
		"clang version 18.1.8": "18.1.8",
		"Microsoft (R) C/C++ Optimizing Compiler Version 19.44.35207 for x64": "19.44.35207",
		"Cuda compilation tools, release 12.4, V12.4.131":                     "12.4",
		"g++ (Rev3) 13.2.0": "13.2.0",
		"clang version 18":  "",
	}
	for input, want := range cases {
		got, ok := ExtractVersion(input)
		if want == "" {
			if ok {
				t.Fatalf("ExtractVersion(%q) = %q, want no match", input, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("ExtractVersion(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
}

func TestHighestVersion(t *testing.T) {
	got, ok := HighestVersion([]string{"12.4", "13.10", "13.9", "unknown"})
	if !ok || got != "13.10" {
		t.Fatalf("HighestVersion = %q, %v", got, ok)
	}
	if _, ok := HighestVersion(nil); ok {
		t.Fatal("no versions must report no result")
	}
}

func TestIsLocalPath(t *testing.T) {
	local := []string{`D:\LLVM\bin\clang-cl.exe`, "/usr/bin/g++", "./clang++", `C:\vs2022\vcvars64.bat`, "~/toolchain/clang"}
	for _, value := range local {
		if !IsLocalPath(value) {
			t.Fatalf("%q should be recognised as a machine-local path", value)
		}
	}
	portable := []string{"", "auto", "clang-cl", "12.0~12.9", "14.3~14.5", "g++"}
	for _, value := range portable {
		if IsLocalPath(value) {
			t.Fatalf("%q should be treated as a portable constraint", value)
		}
	}
}

func TestPortableSelectorDropsMachinePaths(t *testing.T) {
	cases := map[string]string{
		`D:\LLVM\bin\clang-cl.exe`:   "clang-cl",
		`D:\LLVM\bin\clang++.exe`:    "clang++",
		"/usr/bin/g++-13":            "auto",
		`D:\vs2022\VC\bin\cl.exe`:    "cl",
		"clang++":                    "clang++",
		"18~20":                      "18~20",
		"":                           "auto",
		`D:\custom\toolchain\cc.exe`: "auto",
	}
	for input, want := range cases {
		if got := PortableSelector(input); got != want {
			t.Fatalf("PortableSelector(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMSVCToolsetVersionFromPath(t *testing.T) {
	got, ok := MSVCToolsetVersion(`D:\vs2022\VC\Tools\MSVC\14.44.35207\bin\HostX64\x64\cl.exe`)
	if !ok || got != "14.44.35207" {
		t.Fatalf("MSVCToolsetVersion = %q, %v", got, ok)
	}
	if _, ok := MSVCToolsetVersion(`D:\LLVM\bin\clang-cl.exe`); ok {
		t.Fatal("a Clang path has no MSVC toolset version")
	}
}

func TestToolkitVersionFromPath(t *testing.T) {
	if got, ok := ToolkitVersionFromPath(`C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v12.6`); !ok || got != "12.6" {
		t.Fatalf("versioned directory = %q, %v", got, ok)
	}
	if got, ok := ToolkitVersionFromPath("/usr/local/cuda-12.8/"); !ok || got != "12.8" {
		t.Fatalf("linux directory = %q, %v", got, ok)
	}
	if _, ok := ToolkitVersionFromPath("D:/CUDA"); ok {
		t.Fatal("a plain directory name carries no version")
	}
}
