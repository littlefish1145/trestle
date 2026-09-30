package vcpkg

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type TripletRequest struct {
	OS             string
	Arch           string
	Compiler       string
	ABI            string
	CRTLinkage     string
	LibraryLinkage string
	Available      []string
}

type TripletCandidate struct {
	Name  string
	Score int
	Why   []string
}

func RankTriplets(request TripletRequest) ([]TripletCandidate, error) {
	available := append([]string{}, request.Available...)
	sort.Strings(available)
	arch := request.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	if arch == "amd64" {
		arch = "x64"
	}
	if arch == "386" {
		arch = "x86"
	}
	osName := request.OS
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
	var result []TripletCandidate
	for _, name := range available {
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, arch+"-"+osName) {
			continue
		}
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
