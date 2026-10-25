package toolchain

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CompilerSTLCompatibilityDefine is the macro the MSVC STL documents for
// suppressing its compiler-version guard. Trestle passes it when a Clang build
// targeting the MSVC ABI is older than the installed MSVC STL accepts, so the
// common toolchain pair (for example Clang 18 with MSVC 14.44 headers) can
// still compile instead of failing with STL1000 before any user code is read.
const CompilerSTLCompatibilityDefine = "_ALLOW_COMPILER_AND_STL_VERSION_MISMATCH"

var (
	clangMajorPattern = regexp.MustCompile(`(\d+)(?:\.\d+)*`)
	clangGatePattern  = regexp.MustCompile(`__clang_major__\s*<\s*(\d+)`)
)

// ClangMSVCSTLCompatibilityFlag returns the compiler flag that keeps Clang able
// to use the installed MSVC STL, or "" when no workaround is needed. The
// required Clang major version is read from the STL's own yvals_core.h, so the
// decision follows whatever Visual Studio version the environment provides.
func ClangMSVCSTLCompatibilityFlag(t Toolchain) string {
	if t.Kind != Clang || !targetUsesMSVCABI(t.Target) {
		return ""
	}
	major := firstNumber(t.Version)
	if major == 0 {
		return ""
	}
	required := msvcSTLRequiredClangMajor(t.Env["INCLUDE"])
	if required == 0 || major >= required {
		return ""
	}
	return "-D" + CompilerSTLCompatibilityDefine
}

// firstNumber extracts the leading integer of a version string such as
// "18.1.8" or "clang version 18.1.8".
func firstNumber(version string) int {
	match := clangMajorPattern.FindString(version)
	if match == "" {
		return 0
	}
	value, err := strconv.Atoi(strings.SplitN(match, ".", 2)[0])
	if err != nil {
		return 0
	}
	return value
}

// msvcSTLRequiredClangMajor reads the minimum Clang version from the MSVC STL
// header found on the include path. Multiple include roots are searched because
// the compiler environment may list several SDKs.
func msvcSTLRequiredClangMajor(include string) int {
	for _, dir := range filepath.SplitList(include) {
		if dir == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "yvals_core.h"))
		if err != nil {
			continue
		}
		text := string(data)
		guard := strings.Index(text, CompilerSTLCompatibilityDefine)
		if guard < 0 {
			continue
		}
		match := clangGatePattern.FindStringSubmatch(text[guard:])
		if match == nil {
			continue
		}
		if value, err := strconv.Atoi(match[1]); err == nil {
			return value
		}
	}
	return 0
}
