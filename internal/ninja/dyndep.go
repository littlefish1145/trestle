package ninja

import (
	"bytes"
	"fmt"
)

// Dyndep describes a Ninja dyndep file: its version, optional build name, and the
// bind and implicit dependency edges discovered after the initial manifest was
// written.
type Dyndep struct {
	Version  int
	Build    string
	Inputs   []string
	Outputs  []string
	Restat   bool
	Binds    map[string][]string
	Implicit map[string][]string
}

// EmitDyndep renders dyndep as the contents of a Ninja dynamic-dependency file,
// escaping every path and rejecting unsupported versions.
func EmitDyndep(dyndep Dyndep) ([]byte, error) {
	if dyndep.Version != 1 {
		return nil, fmt.Errorf("unsupported dyndep version %d", dyndep.Version)
	}
	var output bytes.Buffer
	writeDyndepHeader(&output, dyndep)
	writeDyndepEdges(&output, "bind", dyndep.Binds)
	writeDyndepEdges(&output, "implicit", dyndep.Implicit)
	return output.Bytes(), nil
}

// writeDyndepHeader writes the version line and the optional build, restat,
// inputs, and outputs bindings that precede a dyndep file's build statements.
func writeDyndepHeader(output *bytes.Buffer, dyndep Dyndep) {
	fmt.Fprintf(output, "ninja_dyndep_version = %d\n", dyndep.Version)
	if dyndep.Build != "" {
		fmt.Fprintf(output, "build = %s\n", EscapePath(dyndep.Build))
	}
	if dyndep.Restat {
		output.WriteString("restat = 1\n")
	}
	writeDyndepList(output, "inputs", dyndep.Inputs)
	writeDyndepList(output, "outputs", dyndep.Outputs)
}

// writeDyndepList writes a "keyword = value" line whose value is the
// space-separated, escaped paths, and is skipped entirely when values is empty.
func writeDyndepList(output *bytes.Buffer, keyword string, values []string) {
	if len(values) == 0 {
		return
	}
	output.WriteString(keyword)
	output.WriteString(" = ")
	for i, value := range values {
		if i > 0 {
			output.WriteByte(' ')
		}
		output.WriteString(EscapePath(value))
	}
	output.WriteByte('\n')
}

// writeDyndepEdges writes one "build <path>: dyndep" block per entry in edges,
// listing the discovered paths under keyword ("bind" or "implicit"). Entries are
// emitted in Go's unspecified map iteration order.
func writeDyndepEdges(output *bytes.Buffer, keyword string, edges map[string][]string) {
	for outputPath, inputs := range edges {
		fmt.Fprintf(output, "build %s: dyndep\n", EscapePath(outputPath))
		fmt.Fprintf(output, "  %s = %s\n", keyword, joinEscaped(inputs))
	}
}

func joinEscaped(values []string) string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = EscapePath(value)
	}
	return bytes.NewBufferString(join(result, " ")).String()
}
