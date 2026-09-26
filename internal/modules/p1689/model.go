package p1689

import (
	"encoding/json"
	"fmt"
	"io"
)

type Document struct {
	Version  int    `json:"version"`
	Revision int    `json:"revision,omitempty"`
	Rules    []Rule `json:"rules"`
}

type Rule struct {
	PrimaryOutput string   `json:"primary-output,omitempty"`
	Outputs       []string `json:"outputs,omitempty"`
	Provides      []Module `json:"provides,omitempty"`
	Requires      []Module `json:"requires,omitempty"`
}

type Module struct {
	LogicalName        string `json:"logical-name"`
	SourcePath         string `json:"source-path,omitempty"`
	CompiledModulePath string `json:"compiled-module-path,omitempty"`
	UniqueOnSourcePath bool   `json:"unique-on-source-path,omitempty"`
	LookupMethod       string `json:"lookup-method,omitempty"`
	IsInterface        *bool  `json:"is-interface,omitempty"`
}

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

func (m Module) Key() ModuleKey {
	return ModuleKey{LogicalName: m.LogicalName, SourcePath: m.SourcePath, BySource: m.UniqueOnSourcePath}
}

type ModuleKey struct {
	LogicalName string
	SourcePath  string
	BySource    bool
}

func (k ModuleKey) String() string {
	if k.BySource {
		return k.SourcePath + "::" + k.LogicalName
	}
	return k.LogicalName
}
