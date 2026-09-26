package modules

import (
	"context"

	"trestle/internal/modules/p1689"
)

type ArtifactKind string

const (
	PCM ArtifactKind = "pcm"
	IFC ArtifactKind = "ifc"
	CMI ArtifactKind = "cmi"
)

type Artifact struct {
	LogicalName string
	Path        string
	Kind        ArtifactKind
}

type Reference struct {
	LogicalName string
	Path        string
}

type Support interface {
	Scan(context.Context, string) (p1689.Document, error)
	CompileModule(source, output string, references []Reference) (string, []string, error)
	ConsumerArgs(references []Reference) ([]string, error)
}

type ObjectCompiler interface {
	CompileModuleObject(source, output string, references []Reference) (string, []string, error)
}
