package ninja

import (
	"bytes"
	"fmt"
)

type Dyndep struct {
	Version  int
	Build    string
	Inputs   []string
	Outputs  []string
	Restat   bool
	Binds    map[string][]string
	Implicit map[string][]string
}

func EmitDyndep(dyndep Dyndep) ([]byte, error) {
	if dyndep.Version != 1 {
		return nil, fmt.Errorf("unsupported dyndep version %d", dyndep.Version)
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "ninja_dyndep_version = %d\n", dyndep.Version)
	if dyndep.Build != "" {
		fmt.Fprintf(&output, "build = %s\n", EscapePath(dyndep.Build))
	}
	if dyndep.Restat {
		output.WriteString("restat = 1\n")
	}
	if len(dyndep.Inputs) > 0 {
		output.WriteString("inputs = ")
		for i, input := range dyndep.Inputs {
			if i > 0 {
				output.WriteByte(' ')
			}
			output.WriteString(EscapePath(input))
		}
		output.WriteByte('\n')
	}
	if len(dyndep.Outputs) > 0 {
		output.WriteString("outputs = ")
		for i, item := range dyndep.Outputs {
			if i > 0 {
				output.WriteByte(' ')
			}
			output.WriteString(EscapePath(item))
		}
		output.WriteByte('\n')
	}
	for outputPath, inputs := range dyndep.Binds {
		fmt.Fprintf(&output, "build %s: dyndep\n", EscapePath(outputPath))
		fmt.Fprintf(&output, "  bind = %s\n", joinEscaped(inputs))
	}
	for outputPath, inputs := range dyndep.Implicit {
		fmt.Fprintf(&output, "build %s: dyndep\n", EscapePath(outputPath))
		fmt.Fprintf(&output, "  implicit = %s\n", joinEscaped(inputs))
	}
	return output.Bytes(), nil
}

func joinEscaped(values []string) string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = EscapePath(value)
	}
	return bytes.NewBufferString(join(result, " ")).String()
}
