package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/inferLean/inferlean-project/internal/analyzer"
	"github.com/inferLean/inferlean-project/internal/model"
)

func runIntent(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("intent", flag.ContinueOnError)
	fs.SetOutput(stderr)

	outputPath := fs.String("output", "workload-intent.json", "")
	advanced := fs.Bool("advanced", false, "")

	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: inferLean intent [flags]")
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Flags:")
		fmt.Fprintln(stderr, "  --output <path>   Write the intent JSON to this path (default: workload-intent.json)")
		fmt.Fprintln(stderr, "  --advanced        Ask optional cache-reuse questions for prefix and multimodal caching")
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpRequested
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected argument: %s", fs.Arg(0))
	}

	reader := bufio.NewReader(cliInput)
	profile := &model.WorkloadProfile{
		SchemaVersion:  model.WorkloadProfileSchemaVersion,
		Source:         model.WorkloadProfileSourceUserInput,
		ServingPattern: model.ServingPatternUnknown,
		TaskPattern:    model.TaskPatternUnknown,
		Objective:      string(analyzer.BalancedIntent),
		PrefixReuse:    model.WorkloadProfileReuseUnknown,
		MediaReuse:     model.WorkloadProfileReuseUnknown,
	}

	fmt.Fprintln(stdout, "Answer a few questions about the node's intended workload.")
	fmt.Fprintln(stdout, "Press Enter to accept the default shown in parentheses.")
	if *advanced {
		fmt.Fprintln(stdout, "Advanced mode includes cache-reuse questions for repeated prompts and repeated media inputs.")
	}
	fmt.Fprintln(stdout, "")

	objective, err := promptChoice(
		reader,
		stdout,
		"Optimization priority [balanced/latency/throughput]",
		"balanced",
		map[string]string{
			"balanced":   string(analyzer.BalancedIntent),
			"latency":    string(analyzer.LatencyFirstIntent),
			"throughput": string(analyzer.ThroughputFirstIntent),
		},
	)
	if err != nil {
		return err
	}
	profile.Objective = objective

	servingPattern, err := promptChoice(
		reader,
		stdout,
		"Traffic shape [skip/realtime/batch/mixed]",
		"mixed",
		map[string]string{
			"skip":     model.ServingPatternUnknown,
			"realtime": model.ServingPatternRealtimeChat,
			"batch":    model.ServingPatternOfflineBatch,
			"mixed":    model.ServingPatternMixed,
		},
	)
	if err != nil {
		return err
	}
	profile.ServingPattern = servingPattern

	if *advanced {
		prefixReuse, err := promptChoice(
			reader,
			stdout,
			"Do repeated prompt templates or long shared prefixes matter here? [skip/high/low]",
			"skip",
			map[string]string{
				"skip": model.WorkloadProfileReuseUnknown,
				"high": model.WorkloadProfileReuseHigh,
				"low":  model.WorkloadProfileReuseLow,
			},
		)
		if err != nil {
			return err
		}
		profile.PrefixReuse = prefixReuse

		mediaReuse, err := promptChoice(
			reader,
			stdout,
			"Do repeated image or video inputs matter here? [skip/high/low]",
			"skip",
			map[string]string{
				"skip": model.WorkloadProfileReuseUnknown,
				"high": model.WorkloadProfileReuseHigh,
				"low":  model.WorkloadProfileReuseLow,
			},
		)
		if err != nil {
			return err
		}
		profile.MediaReuse = mediaReuse
	}

	absOutput, err := filepath.Abs(*outputPath)
	if err != nil {
		return err
	}
	if err := analyzer.SaveJSON(absOutput, profile); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, absOutput)
	return nil
}

func promptChoice(reader *bufio.Reader, out io.Writer, label, defaultValue string, values map[string]string) (string, error) {
	for {
		fmt.Fprintf(out, "%s (%s): ", label, defaultValue)
		line, err := promptLine(reader)
		if err != nil {
			return "", err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "" {
			answer = defaultValue
		}
		if normalized, ok := values[answer]; ok {
			return normalized, nil
		}
		fmt.Fprintf(out, "Please choose one of: %s\n", strings.Join(sortedKeys(values), ", "))
	}
}

func promptLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimRight(line, "\r\n")
	if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
		return "", io.EOF
	}
	return line, nil
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
