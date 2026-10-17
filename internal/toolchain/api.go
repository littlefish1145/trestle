// Package toolchain discovers, resolves, and describes the C/C++ and CUDA
// toolchains available on a machine, so trestle.toml can name portable version
// ranges instead of absolute paths.
package toolchain

import "context"

// Kind identifies the compiler family a Toolchain belongs to.
type Kind string

// The compiler families trestle knows how to drive.
const (
	GCC   Kind = "gcc"
	Clang Kind = "clang"
	MSVC  Kind = "msvc"
)

// Toolchain is a detected, ready-to-run compiler set: the executables trestle
// invokes, the target they build for, and any environment or runner needed to
// execute them.
type Toolchain struct {
	Kind       Kind
	CC         string
	CXX        string
	Archiver   string
	Linker     string
	Version    string
	Target     string
	Env        map[string]string
	Setup      string
	Runner     string
	RunnerArgs []string
}

// Wrap rewrites a command so it runs through the toolchain's runner, such as
// wsl.exe, returning the executable and arguments to spawn.
func (t Toolchain) Wrap(executable string, args []string) (string, []string) {
	if t.Runner == "" {
		return executable, args
	}
	wrapped := append([]string{}, t.RunnerArgs...)
	wrapped = append(wrapped, "--exec", executable)
	wrapped = append(wrapped, args...)
	return t.Runner, wrapped
}

// Has reports whether the toolchain's compiler family matches kind.
func (t Toolchain) Has(kind Kind) bool {
	return t.Kind == kind
}

// CompileSpec describes one translation unit to compile.
type CompileSpec struct {
	Source      string
	Output      string
	Includes    []string
	Defines     []string
	Options     []string
	CStandard   string
	CXXStandard string
	Depfile     string
}

// LinkSpec describes one link step that produces an executable or library.
type LinkSpec struct {
	Inputs        []string
	Output        string
	ImportLibrary string
	Shared        bool
	LibraryDirs   []string
	Libraries     []string
	Options       []string
}

// Detector resolves a requested compiler specification into a usable
// Toolchain.
type Detector interface {
	Detect(context.Context, string) (Toolchain, error)
}
