package toolchain

import "context"

type Kind string

const (
	GCC   Kind = "gcc"
	Clang Kind = "clang"
	MSVC  Kind = "msvc"
)

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

func (t Toolchain) Wrap(executable string, args []string) (string, []string) {
	if t.Runner == "" {
		return executable, args
	}
	wrapped := append([]string{}, t.RunnerArgs...)
	wrapped = append(wrapped, "--exec", executable)
	wrapped = append(wrapped, args...)
	return t.Runner, wrapped
}

func (t Toolchain) Has(kind Kind) bool {
	return t.Kind == kind
}

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

type LinkSpec struct {
	Inputs        []string
	Output        string
	ImportLibrary string
	Shared        bool
	LibraryDirs   []string
	Libraries     []string
	Options       []string
}

type Detector interface {
	Detect(context.Context, string) (Toolchain, error)
}
