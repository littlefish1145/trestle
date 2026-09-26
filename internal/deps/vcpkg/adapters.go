package vcpkg

import "trestle/internal/model"

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

func ResolveAdapter(port string, layout Layout) (model.Usage, bool) {
	adapter, ok := adapters[port]
	if !ok {
		return model.Usage{}, false
	}
	return adapter(layout)
}
