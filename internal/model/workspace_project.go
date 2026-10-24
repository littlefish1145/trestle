package model

// MemberProject is one resolved workspace member: the member's name, the
// directory it was resolved from, the per-member build directory its artifacts
// and manifest live in, and its resolved graph.
type MemberProject struct {
	Name     string
	Root     string
	BuildDir string
	Project  ResolvedProject
	// DependsOn lists the members this member depends on, sorted by name.
	DependsOn []string
}

// WorkspaceProject is the resolved view of a workspace: every member graph, a
// flattened global target list, and the member-level build order. Target IDs
// are globally unique, so ByID addresses a target without naming its member;
// MemberOf recovers the owning member.
type WorkspaceProject struct {
	// Members holds each resolved member in declaration order (root last when
	// it declares targets).
	Members []MemberProject
	// Order lists member names such that a member's dependencies appear before
	// it, so sequential builds see every artifact they consume.
	Order []string
	// Targets is the flat list of every target in the workspace.
	Targets []ResolvedTarget
	// ByID indexes the flat target list.
	ByID map[TargetID]ResolvedTarget
	// MemberOf maps a target ID to the member that declares it.
	MemberOf map[TargetID]string
}

// Member returns the member with the given name, or false.
func (w WorkspaceProject) Member(name string) (MemberProject, bool) {
	for _, member := range w.Members {
		if member.Name == name {
			return member, true
		}
	}
	return MemberProject{}, false
}

// MemberForTarget returns the member that declares id.
func (w WorkspaceProject) MemberForTarget(id TargetID) (string, bool) {
	name, ok := w.MemberOf[id]
	return name, ok
}
