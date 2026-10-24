package toolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Compile builds the argument vector for compiling one source file, choosing
// MSVC or GNU-style flags from the toolchain kind.
func (t Toolchain) Compile(spec CompileSpec) (string, []string, error) {
	if t.CXX == "" {
		return "", nil, fmt.Errorf("C++ compiler is not configured")
	}
	compiler := t.CXX
	if filepath.Ext(spec.Source) == ".c" && t.CC != "" {
		compiler = t.CC
	}
	var args []string
	if t.Kind == MSVC {
		args = msvcCompileArgs(spec)
	} else {
		args = gnuCompileArgs(spec)
	}
	exe, args := t.Wrap(compiler, args)
	return exe, args, nil
}

// msvcCompileArgs builds the cl.exe argument vector for one translation unit,
// selecting C or C++ standard flags from the source extension.
func msvcCompileArgs(spec CompileSpec) []string {
	args := []string{"/nologo", "/utf-8", "/c", spec.Source, "/Fo" + spec.Output}
	if filepath.Ext(spec.Source) == ".c" {
		if spec.CStandard != "" {
			args = append(args, "/std:c"+strings.TrimPrefix(spec.CStandard, "c"))
		}
	} else {
		args = append(args, "/EHsc")
		if spec.CXXStandard != "" {
			args = append(args, msvcStandard(spec.CXXStandard))
		}
	}
	for _, include := range spec.Includes {
		args = append(args, "/I"+include)
	}
	for _, define := range spec.Defines {
		args = append(args, "/D"+define)
	}
	args = append(args, "/showIncludes")
	args = append(args, spec.Options...)
	return args
}

// gnuCompileArgs builds the GNU-style argument vector for one translation unit,
// including the dependency-file flags when a depfile was requested.
func gnuCompileArgs(spec CompileSpec) []string {
	args := []string{}
	if filepath.Ext(spec.Source) == ".c" {
		args = append(args, "-x", "c", "-std="+spec.CStandard)
	} else {
		args = append(args, "-x", "c++", "-std="+spec.CXXStandard)
	}
	for _, include := range spec.Includes {
		args = append(args, "-I"+include)
	}
	for _, define := range spec.Defines {
		args = append(args, "-D"+define)
	}
	if spec.Depfile != "" {
		args = append(args, "-MMD", "-MF", spec.Depfile)
	}
	args = append(args, "-c", spec.Source, "-o", spec.Output)
	args = append(args, spec.Options...)
	return args
}

// Link builds the argument vector that links the inputs into an executable or a
// shared library. For MSVC, a compiler driver such as cl.exe is replaced by the
// link.exe installed beside it when present: the driver shells out to the
// linker through a temporary response file, which fails with D8018 on machines
// where that file cannot be created. Invoking the linker directly avoids the
// intermediate file and the same objects, libraries and options are used.
func (t Toolchain) Link(spec LinkSpec) (string, []string, error) {
	linker := t.Linker
	if linker == "" {
		linker = t.CXX
	}
	var args []string
	if t.Kind == MSVC {
		if msvcDriverLinker(linker) {
			if native := msvcNativeLinker(linker); native != "" {
				linker = native
				args = msvcNativeLinkArgs(spec)
			} else {
				args = msvcDriverLinkArgs(spec)
			}
		} else {
			args = msvcNativeLinkArgs(spec)
		}
	} else {
		args = gnuLinkArgs(spec)
	}
	exe, args := t.Wrap(linker, args)
	return exe, args, nil
}

// msvcNativeLinker returns link.exe from the compiler driver's own toolset
// directory. Only an absolute driver path is considered, so a bare "cl" that
// resolves through PATH is left to the driver form.
func msvcNativeLinker(driver string) string {
	if !filepath.IsAbs(driver) {
		return ""
	}
	dir := filepath.Dir(driver)
	for _, name := range []string{"link.exe", "lld-link.exe"} {
		candidate := filepath.Join(dir, name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// msvcDriverLinker reports whether linker is the MSVC compiler driver (cl or
// clang-cl), which links with /Fe and /LD rather than the native link.exe flags.
func msvcDriverLinker(linker string) bool {
	base := strings.ToLower(filepath.Base(linker))
	return base == "cl" || base == "cl.exe" || strings.Contains(base, "clang-cl")
}

// msvcDriverLinkArgs builds the argument vector a compiler driver such as cl or
// clang-cl accepts, where linker-only flags must follow a /link marker.
func msvcDriverLinkArgs(spec LinkSpec) []string {
	args := []string{"/nologo", "/Fe" + spec.Output}
	if spec.Shared {
		args = append(args, "/LD")
	}
	args = append(args, spec.Inputs...)
	for _, library := range spec.Libraries {
		if name := msvcLibrary(library); name != "" {
			args = append(args, name)
		}
	}
	if spec.ImportLibrary != "" || len(spec.LibraryDirs) > 0 || len(spec.Options) > 0 {
		args = append(args, "/link")
		if spec.ImportLibrary != "" {
			args = append(args, "/IMPLIB:"+spec.ImportLibrary)
		}
		for _, dir := range spec.LibraryDirs {
			args = append(args, "/LIBPATH:"+dir)
		}
		args = append(args, spec.Options...)
	}
	return args
}

// msvcNativeLinkArgs builds the argument vector link.exe accepts directly.
func msvcNativeLinkArgs(spec LinkSpec) []string {
	args := []string{"/nologo", "/OUT:" + spec.Output}
	if spec.Shared {
		args = append(args, "/dll")
	}
	if spec.ImportLibrary != "" {
		args = append(args, "/IMPLIB:"+spec.ImportLibrary)
	}
	for _, dir := range spec.LibraryDirs {
		args = append(args, "/LIBPATH:"+dir)
	}
	args = append(args, spec.Inputs...)
	for _, library := range spec.Libraries {
		if name := msvcLibrary(library); name != "" {
			args = append(args, name)
		}
	}
	args = append(args, spec.Options...)
	return args
}

// gnuLinkArgs builds the argument vector a GNU-style linker accepts, mapping a
// bare library name to -l<name> and passing -prefixed names through untouched.
func gnuLinkArgs(spec LinkSpec) []string {
	args := []string{}
	if spec.Shared {
		args = append(args, "-shared")
	}
	for _, dir := range spec.LibraryDirs {
		args = append(args, "-L"+dir)
	}
	args = append(args, spec.Inputs...)
	for _, library := range spec.Libraries {
		if strings.HasPrefix(library, "-") {
			args = append(args, library)
		} else {
			args = append(args, "-l"+library)
		}
	}
	args = append(args, "-o", spec.Output)
	args = append(args, spec.Options...)
	return args
}

func msvcLibrary(library string) string {
	// A CMake import configured with a GNU-like compiler may expose
	// Threads::Threads as pthread(s). MSVC implements std::thread through its
	// runtime and has no corresponding pthread import library.
	if strings.EqualFold(library, "pthread") || strings.EqualFold(library, "pthreads") {
		return ""
	}
	if strings.EqualFold(filepath.Ext(library), ".lib") {
		return library
	}
	return library + ".lib"
}

// Archive builds the argument vector that combines the inputs into the static
// library named output.
func (t Toolchain) Archive(output string, inputs []string) (string, []string, error) {
	if t.Kind == MSVC {
		exe, args := t.Wrap(t.Archiver, append([]string{"/nologo", "/OUT:" + output}, inputs...))
		return exe, args, nil
	}
	exe, args := t.Wrap(t.Archiver, append([]string{"rcs", output}, inputs...))
	return exe, args, nil
}

func msvcStandard(value string) string {
	switch value {
	case "c++17", "gnu++17":
		return "/std:c++17"
	case "c++20", "c++2a":
		return "/std:c++20"
	case "c++23", "c++2b":
		return "/std:c++latest"
	case "gnu++20":
		return "/std:c++20"
	default:
		return "/std:c++latest"
	}
}
