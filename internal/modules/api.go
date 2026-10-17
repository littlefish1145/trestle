// Package modules implements C++ module dependency scanning: it asks a
// compiler-specific scanner for a p1689 dependency document, caches the result,
// and assembles the documents into a build graph.
package modules

import (
	"context"

	"trestle/internal/modules/p1689"
)

// ArtifactKind identifies the on-disk form of a compiled C++ module interface.
type ArtifactKind string

// The artifact kinds trestle can consume: Clang's precompiled module (PCM),
// MSVC's interface (IFC), and a generic module interface (CMI).
const (
	PCM ArtifactKind = "pcm"
	IFC ArtifactKind = "ifc"
	CMI ArtifactKind = "cmi"
)

// Artifact pairs a module's logical name with the compiled interface file that
// other translation units import.
type Artifact struct {
	LogicalName string
	Path        string
	Kind        ArtifactKind
}

// Reference points a consumer at an already-compiled module interface by
// logical name and path.
type Reference struct {
	LogicalName string
	Path        string
}

// Support is the backend contract for C++ modules: scanning a source for its
// module dependencies, building a provider's interface, and translating a
// consumer's references into compiler arguments.
type Support interface {
	Scan(context.Context, string) (p1689.Document, error)
	CompileModule(source, output string, references []Reference) (string, []string, error)
	ConsumerArgs(references []Reference) ([]string, error)
}

// ObjectCompiler is the optional contract for backends that can compile a
// module interface directly into an object file, as MSVC and GCC do.
type ObjectCompiler interface {
	CompileModuleObject(source, output string, references []Reference) (string, []string, error)
}
