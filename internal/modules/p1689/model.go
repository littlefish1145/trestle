// Package p1689 decodes the module dependency information that compilers emit
// in the P1689 JSON format, the format CMake and Ninja use for C++20 modules.
package p1689

import (
	"encoding/json"
	"fmt"
	"io"
)

// Document is a decoded P1689 file: a version plus one or more rules describing
// how a translation unit provides and requires modules.
type Document struct {
	Version  int    `json:"version"`
	Revision int    `json:"revision,omitempty"`
	Rules    []Rule `json:"rules"`
}

// Rule is one translation unit's module information, listing its outputs and
// the modules it provides and requires.
type Rule struct {
	PrimaryOutput string   `json:"primary-output,omitempty"`
	Outputs       []string `json:"outputs,omitempty"`
	Provides      []Module `json:"provides,omitempty"`
	Requires      []Module `json:"requires,omitempty"`
}

// Module describes a single module relationship: the logical name, where it
// lives on disk, and how the compiler should look it up.
type Module struct {
	LogicalName        string `json:"logical-name"`
	SourcePath         string `json:"source-path,omitempty"`
	CompiledModulePath string `json:"compiled-module-path,omitempty"`
	UniqueOnSourcePath bool   `json:"unique-on-source-path,omitempty"`
	LookupMethod       string `json:"lookup-method,omitempty"`
	IsInterface        *bool  `json:"is-interface,omitempty"`
}

// Decode reads a P1689 document and validates it, returning an error for an
// unsupported version or a rule with a missing logical name.
func Decode(reader io.Reader) (Document, error) {
	var document Document
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&document); err != nil {
		return Document{}, err
	}
	if document.Version != 1 {
		return Document{}, fmt.Errorf("E_MODULE_FORMAT: unsupported P1689 version %d", document.Version)
	}
	for i, rule := range document.Rules {
		if rule.PrimaryOutput == "" && len(rule.Provides) == 0 && len(rule.Requires) == 0 {
			return Document{}, fmt.Errorf("E_MODULE_FORMAT: rule %d is empty", i)
		}
		for _, module := range append(append([]Module{}, rule.Provides...), rule.Requires...) {
			if module.LogicalName == "" {
				return Document{}, fmt.Errorf("E_MODULE_FORMAT: rule %d has an empty logical-name", i)
			}
		}
	}
	return document, nil
}

// Key returns the identifier used to deduplicate this module, which includes
// its source path when the module is only unique per source file.
func (m Module) Key() ModuleKey {
	return ModuleKey{LogicalName: m.LogicalName, SourcePath: m.SourcePath, BySource: m.UniqueOnSourcePath}
}

// ModuleKey is the comparable identity of a module, formed from its logical
// name and, when required, its source path.
type ModuleKey struct {
	LogicalName string
	SourcePath  string
	BySource    bool
}

// String renders the key as a stable string, joining the source path and
// logical name for by-source keys.
func (k ModuleKey) String() string {
	if k.BySource {
		return k.SourcePath + "::" + k.LogicalName
	}
	return k.LogicalName
}
