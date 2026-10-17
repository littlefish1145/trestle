package vcpkg

import "trestle/internal/model"

// PortAdapter derives usage requirements for a port that vcpkg installs but
// pkg-config cannot describe, such as a header-only library. It returns false
// when the port is not handled.
type PortAdapter func(Layout) (model.Usage, bool)

var adapters = map[string]PortAdapter{
	"glm": func(layout Layout) (model.Usage, bool) {
		return model.Usage{Compile: model.CompileUsage{IncludeDirs: []string{layout.IncludeDir}}}, true
	},
	"eigen3": func(layout Layout) (model.Usage, bool) {
		return model.Usage{Compile: model.CompileUsage{IncludeDirs: []string{layout.IncludeDir}}}, true
	},
	"nlohmann-json": func(layout Layout) (model.Usage, bool) {
		return model.Usage{Compile: model.CompileUsage{IncludeDirs: []string{layout.IncludeDir}}}, true
	},
}

// ResolveAdapter applies the built-in adapter registered for a port, if any.
func ResolveAdapter(port string, layout Layout) (model.Usage, bool) {
	adapter, ok := adapters[port]
	if !ok {
		return model.Usage{}, false
	}
	return adapter(layout)
}
