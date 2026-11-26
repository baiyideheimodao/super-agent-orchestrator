package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var rolePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{2,30}$`)

type spawnArgs struct {
	Role    string        `json:"role"`
	Task    string        `json:"task"`
	Context ContextPacket `json:"context"`
}

type submitArgs struct {
	Summary   string         `json:"summary"`
	Artifact  string         `json:"artifact"`
	SelfCheck map[string]any `json:"self_check"`
}

type finishArgs struct {
	FinalAnswer string `json:"final_answer"`
}

func parseSpawnArgs(raw string) (spawnArgs, error) {
	var args spawnArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return args, fmt.Errorf("invalid JSON for spawn_agent: %w", err)
	}
	args.Role = strings.TrimSpace(args.Role)
	args.Task = strings.TrimSpace(args.Task)
	args.Context = trimContext(args.Context)

	if !rolePattern.MatchString(args.Role) {
		return args, fmt.Errorf("invalid role %q: use lowercase letters, digits and underscore", args.Role)
	}
	if _, ok := roleSpecs[args.Role]; !ok {
		return args, fmt.Errorf("unknown role %q", args.Role)
	}
	if n := utf8.RuneCountInString(args.Task); n < 3 || n > 2000 {
		return args, fmt.Errorf("task length must be between 3 and 2000 runes")
	}
	if strings.TrimSpace(args.Context.Goal) == "" {
		return args, fmt.Errorf("context.goal is required")
	}
	if utf8.RuneCountInString(args.Context.Goal) > 500 {
		return args, fmt.Errorf("context.goal exceeds 500 runes")
	}
	if len(args.Context.Facts) > 12 || len(args.Context.Upstream) > 8 || len(args.Context.Constraints) > 8 {
		return args, fmt.Errorf("context contains too many items")
	}
	for _, fact := range args.Context.Facts {
		if utf8.RuneCountInString(fact) > 300 {
			return args, fmt.Errorf("context.facts item exceeds 300 runes")
		}
	}
	for _, item := range args.Context.Upstream {
		if utf8.RuneCountInString(item) > 500 {
			return args, fmt.Errorf("context.upstream item exceeds 500 runes")
		}
	}
	for _, item := range args.Context.Constraints {
		if utf8.RuneCountInString(item) > 200 {
			return args, fmt.Errorf("context.constraints item exceeds 200 runes")
		}
	}
	return args, nil
}

func trimContext(packet ContextPacket) ContextPacket {
	packet.Goal = strings.TrimSpace(packet.Goal)
	packet.Facts = cleanList(packet.Facts)
	packet.Upstream = cleanList(packet.Upstream)
	packet.Constraints = cleanList(packet.Constraints)
	return packet
}

func cleanList(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func contextSize(packet ContextPacket) int {
	raw, _ := json.Marshal(packet)
	return utf8.RuneCountInString(string(raw))
}

func validateContextBudget(packet ContextPacket, max int) error {
	if size := contextSize(packet); size > max {
		return fmt.Errorf("%w: context is %d chars, limit is %d", errContextTooLarge, size, max)
	}
	return nil
}

func parseSubmitArgs(raw string) (submitArgs, error) {
	var args submitArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return args, fmt.Errorf("invalid JSON for submit_result: %w", err)
	}
	args.Summary = strings.TrimSpace(args.Summary)
	args.Artifact = strings.TrimSpace(args.Artifact)
	if n := utf8.RuneCountInString(args.Summary); n < 10 || n > 2000 {
		return args, fmt.Errorf("summary length must be between 10 and 2000 runes")
	}
	if n := utf8.RuneCountInString(args.Artifact); n < 10 || n > 20000 {
		return args, fmt.Errorf("artifact length must be between 10 and 20000 runes")
	}
	if args.SelfCheck == nil {
		return args, fmt.Errorf("self_check is required")
	}
	complete, ok := args.SelfCheck["complete"].(bool)
	if !ok || !complete {
		return args, fmt.Errorf("self_check.complete must be true")
	}
	notes, _ := args.SelfCheck["notes"].(string)
	if n := utf8.RuneCountInString(strings.TrimSpace(notes)); n < 3 {
		return args, fmt.Errorf("self_check.notes must contain at least 3 runes")
	}
	return args, nil
}

func parseFinishArgs(raw string) (finishArgs, error) {
	var args finishArgs
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return args, fmt.Errorf("invalid JSON for finish: %w", err)
	}
	args.FinalAnswer = strings.TrimSpace(args.FinalAnswer)
	if n := utf8.RuneCountInString(args.FinalAnswer); n < 10 || n > 30000 {
		return args, fmt.Errorf("final_answer length must be between 10 and 30000 runes")
	}
	return args, nil
}

var errContextTooLarge = errors.New(CodeContextTooLarge)
