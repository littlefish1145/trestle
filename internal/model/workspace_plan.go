package model

// WorkspacePlan describes the member and target graph for a build profile.
type WorkspacePlan struct {
	Backend   string            `json:"backend"`
	Project   string            `json:"project"`
	Profile   string            `json:"profile"`
	SourceDir string            `json:"source_dir"`
	BuildDir  string            `json:"build_dir"`
	Members   []WorkspaceGroup  `json:"members"`
	Targets   []WorkspaceTarget `json:"targets"`
}

type WorkspaceGroup struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`
	Goal      string   `json:"goal,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

type WorkspaceTarget struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Member    string   `json:"member"`
	DependsOn []string `json:"depends_on,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
}
