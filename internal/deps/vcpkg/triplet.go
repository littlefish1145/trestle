package vcpkg

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// TripletRequest describes the target a vcpkg triplet must satisfy, so
// RankTriplets can score the triplets installed under a vcpkg root. Empty OS and
// Arch fields are filled from the host before scoring.
type TripletRequest struct {
	OS             string
	Arch           string
	Compiler       string
	ABI            string
	CRTLinkage     string
	LibraryLinkage string
	Available      []string
}

// TripletCandidate is one installed triplet together with the score it earned
// and the human-readable reasons it matched.
type TripletCandidate struct {
	Name  string
	Score int
	Why   []string
}

// RankTriplets scores every installed triplet in request.Available against the
// request and returns the matches ordered by descending score, breaking ties by
// name.
func RankTriplets(request TripletRequest) ([]TripletCandidate, error) {
	available := append([]string{}, request.Available...)
	sort.Strings(available)
	arch, osName := normalizeTripletTarget(request)
	var result []TripletCandidate
	for _, name := range available {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, arch+"-"+osName) {
			continue
		}
		score, why := scoreTriplet(lower, arch, osName, request)
		if score > 0 {
			result = append(result, TripletCandidate{Name: name, Score: score, Why: why})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Score == result[j].Score {
			return result[i].Name < result[j].Name
		}
		return result[i].Score > result[j].Score
	})
	return result, nil
}

// normalizeTripletTarget resolves the architecture and OS tokens a matching
// triplet name must carry. Empty request fields fall back to the host, and Go's
// architecture and OS names are translated into the ones vcpkg uses.
func normalizeTripletTarget(request TripletRequest) (arch, osName string) {
	arch = request.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	if arch == "amd64" {
		arch = "x64"
	}
	if arch == "386" {
		arch = "x86"
	}
	osName = request.OS
	if osName == "" {
		osName = runtime.GOOS
	}
	if osName == "windows" {
		osName = "windows"
		if request.ABI == "mingw" || strings.EqualFold(request.Compiler, "gcc") {
			osName = "mingw"
		}
	} else if osName == "darwin" {
		osName = "osx"
	} else {
		osName = "linux"
	}
	return arch, osName
}

// scoreTriplet scores one lowercased triplet name against a request, returning
// the points it earned and the reasons it matched. The name is assumed to carry
// the request's architecture and OS prefix; the reasons describe each feature the
// name matched.
func scoreTriplet(lower, arch, osName string, request TripletRequest) (int, []string) {
	score := 0
	why := make([]string, 0, 4)
	if strings.Contains(lower, arch) {
		score += 100
		why = append(why, "architecture "+arch)
	}
	if strings.Contains(lower, osName) {
		score += 80
		why = append(why, "OS "+osName)
	}
	if request.Compiler != "" && strings.Contains(lower, strings.ToLower(request.Compiler)) {
		score += 20
		why = append(why, "compiler "+request.Compiler)
	}
	if request.CRTLinkage == "static" && strings.Contains(lower, "static") {
		score += 8
		why = append(why, "static CRT")
	}
	if request.LibraryLinkage == "static" && strings.HasSuffix(lower, "-static") {
		score += 8
		why = append(why, "static libraries")
	}
	return score, why
}

// SelectAutoTriplet chooses the single best triplet for a request. It fails when
// nothing matches or when the top two are tied, so an ambiguous "auto" never
// silently picks one.
func SelectAutoTriplet(request TripletRequest) (string, error) {
	candidates, err := RankTriplets(request)
	if err != nil {
		return "", err
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("E_VCPKG_TRIPLET: no vcpkg triplet matches %s/%s", request.OS, request.Arch)
	}
	if len(candidates) > 1 && candidates[0].Score == candidates[1].Score {
		return "", fmt.Errorf("E_VCPKG_TRIPLET: auto is ambiguous between %s and %s; set triplet explicitly", candidates[0].Name, candidates[1].Name)
	}
	return candidates[0].Name, nil
}

// AvailableTriplets lists the triplets already installed under a vcpkg root,
// sorted by name and filtered to valid triplet names.
func AvailableTriplets(root string) ([]string, error) {
	entries, err := filepath.Glob(filepath.Join(root, "installed", "*"))
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := filepath.Base(entry)
		if ValidTripletName(name) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result, nil
}
