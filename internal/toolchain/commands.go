package toolchain

import (
	"fmt"
	"path/filepath"
	"strings"
)

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
		args = append(args, "/nologo", "/utf-8", "/c", spec.Source, "/Fo"+spec.Output)
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
		exe, args := t.Wrap(compiler, args)
		return exe, args, nil
	}
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
	exe, args := t.Wrap(compiler, args)
	return exe, args, nil
}

func (t Toolchain) Link(spec LinkSpec) (string, []string, error) {
	if t.Kind == MSVC {
		linker := t.Linker
		if linker == "" {
			linker = t.CXX
		}
		base := strings.ToLower(filepath.Base(linker))
		if base == "cl" || base == "cl.exe" || strings.Contains(base, "clang-cl") {
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
			exe, args := t.Wrap(linker, args)
			return exe, args, nil
		}
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
		exe, args := t.Wrap(linker, args)
		return exe, args, nil
	}
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
	linker := t.Linker
	if linker == "" {
		linker = t.CXX
	}
	exe, args := t.Wrap(linker, args)
	return exe, args, nil
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
