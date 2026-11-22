package buildanalyzer

import (
	"bytes"
	"strconv"
	"strings"
)

type Explanation struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// ExplainWriter collects Ninja's diagnostics from the actual build invocation.
// It also forwards the unmodified stream through the existing progress writer.
type ExplainWriter struct {
	run     *Execution
	pending []byte
}

func (run *Execution) ExplainWriter() *ExplainWriter {
	return &ExplainWriter{run: run}
}

func (writer *ExplainWriter) Write(data []byte) (int, error) {
	size := len(data)
	for line := range bytes.SplitSeq(data, []byte{'\n'}) {
		writer.pending = append(writer.pending, line...)
		consumed := len(line)
		if consumed == len(data) {
			break
		}
		writer.Flush()
		data = data[consumed+1:]
	}
	return size, nil
}

func (writer *ExplainWriter) Flush() {
	line := strings.TrimSpace(string(writer.pending))
	if message, ok := strings.CutPrefix(line, "ninja explain: "); ok {
		kind := classifyExplanation(message)
		writer.run.Explanations = append(writer.run.Explanations, Explanation{kind, message})
	}
	if value, ok := strings.CutPrefix(line, "trestle-history: applied "); ok && writer.run.Scheduler == "unconfirmed" {
		if count, err := strconv.Atoi(value); err == nil && count >= 0 {
			writer.run.AppliedWeights = count
			writer.run.Scheduler = "ninja"
			if count > 0 {
				writer.run.Scheduler = "history"
			} else {
				writer.run.Warnings = append(writer.run.Warnings, "History scheduler found no matching native command hashes")
			}
		}
	}
	writer.pending = writer.pending[:0]
}

func classifyExplanation(message string) string {
	switch {
	case strings.HasPrefix(message, "command line changed for "):
		return "command_changed"
	case strings.HasSuffix(message, " is dirty"):
		return "dirty_dependency"
	case strings.Contains(message, "older than most recent input"):
		return "input_newer"
	case strings.Contains(message, "doesn't exist"), strings.Contains(message, "does not exist"):
		return "missing_file"
	case strings.Contains(message, "depfile"), strings.Contains(message, "deps for "), strings.Contains(message, "stored deps info"):
		return "dependency_metadata"
	case strings.Contains(message, "command line not found"):
		return "missing_command"
	default:
		return "ninja"
	}
}

type WhyResult struct {
	Directory    string        `json:"directory"`
	Profile      string        `json:"profile"`
	Outcome      string        `json:"outcome"`
	Explanations []Explanation `json:"explanations"`
}

// Why returns Ninja's original reason text in encounter order.
func (session *Session) Why() []WhyResult {
	result := make([]WhyResult, 0, len(session.Executions))
	for _, run := range session.Executions {
		result = append(result, WhyResult{run.Directory, run.Profile, run.Outcome, append([]Explanation{}, run.Explanations...)})
	}
	return result
}
