package schemas

import (
	"fmt"
	"strconv"
	"strings"

	"novel-server/shared/models"
)

// ParseFirstScenePlain parses plain text from the first scene prompt.
func ParseFirstScenePlain(text string, expectedChoiceCount int, statNames []string, varNames []string) (*models.SceneContent, error) {
	data := &models.SceneContent{
		StoryVariableDefs: make(map[string]string),
		Choices:           make([]models.ChoiceBlock, 0, expectedChoiceCount),
	}
	lines := getNonEmptyTrimmedLines(text)
	readIdx := 0

	// Phase 1: Parse SVD (optional)
	if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "svd:") {
		svdLineContent := strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "svd:"))
		readIdx++
		if svdLineContent != "" {
			parts := strings.SplitN(svdLineContent, ":", 2)
			if len(parts) == 2 {
				varName, varDesc := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
				if varName == "" || varDesc == "" {
					return nil, fmt.Errorf("svd: malformed definition on svd: line: '%s'", svdLineContent)
				}
				data.StoryVariableDefs[varName] = varDesc
			} else {
				return nil, fmt.Errorf("svd: malformed content on svd: line (expected 'name: description'): '%s'", svdLineContent)
			}
		}
		for readIdx < len(lines) {
			if len(lines[readIdx]) >= 2 && lines[readIdx][1] == ':' && (lines[readIdx][0] >= '1' && lines[readIdx][0] <= '0'+byte(expectedChoiceCount)) {
				break
			}
			parts := strings.SplitN(lines[readIdx], ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("svd: expected 'name: description' for variable definition, got: '%s'", lines[readIdx])
			}
			varName, varDesc := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			if varName == "" || varDesc == "" {
				return nil, fmt.Errorf("svd: malformed variable definition (empty name or desc): '%s'", lines[readIdx])
			}
			data.StoryVariableDefs[varName] = varDesc
			readIdx++
		}
	}

	// Phase 2: Parse expectedChoiceCount Choices
	for i := 0; i < expectedChoiceCount; i++ {
		choiceBlock := models.ChoiceBlock{}

		if readIdx >= len(lines) {
			return nil, fmt.Errorf("choice %d: unexpected EOF, expected choice number line (e.g. '%d:'), got: '%s'", i+1, i+1, safeGetLine(lines, readIdx))
		}
		line := lines[readIdx]
		expectedChoiceNumStr := strconv.Itoa(i + 1)
		if !strings.HasPrefix(line, expectedChoiceNumStr+":") {
			return nil, fmt.Errorf("choice %d: expected prefix '%s:', got '%s'", i+1, expectedChoiceNumStr, line)
		}
		readIdx++

		if readIdx >= len(lines) {
			return nil, fmt.Errorf("choice %d: unexpected EOF, expected NPC index", i+1)
		}
		choiceBlock.Char = strings.TrimSpace(lines[readIdx])
		if choiceBlock.Char == "" {
			return nil, fmt.Errorf("choice %d: NPC index/char identifier is empty", i+1)
		}
		readIdx++

		if readIdx >= len(lines) {
			return nil, fmt.Errorf("choice %d: unexpected EOF, expected situation description", i+1)
		}
		choiceBlock.Description = lines[readIdx]
		if choiceBlock.Description == "" {
			return nil, fmt.Errorf("choice %d: situation description is empty", i+1)
		}
		readIdx++

		options := make([]models.SceneOption, 0, 2)
		for j := 0; j < 2; j++ {
			sceneOpt := models.SceneOption{}
			var cons models.Consequences

			if readIdx >= len(lines) || !strings.HasPrefix(lines[readIdx], "ch:") {
				return nil, fmt.Errorf("choice %d, option %d: expected 'ch:', got '%s'", i+1, j+1, safeGetLine(lines, readIdx))
			}
			sceneOpt.Text = strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "ch:"))
			if sceneOpt.Text == "" {
				return nil, fmt.Errorf("choice %d, option %d: ch text is empty", i+1, j+1)
			}
			readIdx++

			if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "sv:") {
				svChanges, err := parseStoryVariableChanges(strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "sv:")), varNames)
				if err != nil {
					return nil, fmt.Errorf("choice %d, option %d: parsing sv: %w", i+1, j+1, err)
				}
				cons.StoryVariables = svChanges
				readIdx++
			}
			if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "cs:") {
				csChanges, err := parseCoreStatChanges(strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "cs:")), statNames)
				if err != nil {
					return nil, fmt.Errorf("choice %d, option %d: parsing cs: %w", i+1, j+1, err)
				}
				cons.CoreStatsChange = csChanges
				readIdx++
			}
			if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "rt:") {
				cons.ResponseText = strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "rt:"))
				readIdx++
			}
			sceneOpt.Consequences = cons
			options = append(options, sceneOpt)
		}
		choiceBlock.Options = options
		data.Choices = append(data.Choices, choiceBlock)
	}

	if len(data.Choices) != expectedChoiceCount {
		return nil, fmt.Errorf("expected to parse %d choices, but found %d", expectedChoiceCount, len(data.Choices))
	}

	if readIdx != len(lines) {
		return nil, fmt.Errorf("trailing content found after %d choices, starting at line: '%s'", expectedChoiceCount, safeGetLine(lines, readIdx))
	}
	return data, nil
}

// ParseNovelCreatorPlain parses plain text from the ongoing gameplay prompt.
func ParseNovelCreatorPlain(text string, expectedChoiceCount int, statNames []string, varNames []string) (*models.SceneContent, error) {
	data := &models.SceneContent{
		StoryVariableDefs: make(map[string]string),
		Choices:           make([]models.ChoiceBlock, 0, expectedChoiceCount),
	}
	lines := getNonEmptyTrimmedLines(text)
	readIdx := 0

	if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "sssf:") {
		val := strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "sssf:"))
		if val == "" {
			return nil, fmt.Errorf("sssf field is empty")
		}
		data.StorySummarySoFar = val
		readIdx++
	} else {
		return nil, fmt.Errorf("expected 'sssf:' field, got: '%s'", safeGetLine(lines, readIdx))
	}

	if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "fd:") {
		val := strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "fd:"))
		if val == "" {
			return nil, fmt.Errorf("fd field is empty")
		}
		data.FutureDirection = val
		readIdx++
	} else {
		return nil, fmt.Errorf("expected 'fd:' field, got: '%s'", safeGetLine(lines, readIdx))
	}

	if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "vis:") {
		val := strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "vis:"))
		if val == "" {
			return nil, fmt.Errorf("vis field is empty")
		}
		data.VarImpactSummary = val
		readIdx++
	} else {
		return nil, fmt.Errorf("expected 'vis:' field, got: '%s'", safeGetLine(lines, readIdx))
	}

	// SVD parsing - same as ParseFirstScenePlain
	if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "svd:") {
		svdLineContent := strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "svd:"))
		readIdx++
		if svdLineContent != "" {
			parts := strings.SplitN(svdLineContent, ":", 2)
			if len(parts) == 2 {
				varName, varDesc := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
				if varName == "" || varDesc == "" {
					return nil, fmt.Errorf("svd: malformed definition on svd: line: '%s'", svdLineContent)
				}
				data.StoryVariableDefs[varName] = varDesc
			} else {
				return nil, fmt.Errorf("svd: malformed content on svd: line (expected 'name: description'): '%s'", svdLineContent)
			}
		}
		for readIdx < len(lines) {
			if len(lines[readIdx]) >= 2 && lines[readIdx][1] == ':' && (lines[readIdx][0] >= '1' && lines[readIdx][0] <= '0'+byte(expectedChoiceCount)) {
				break
			}
			parts := strings.SplitN(lines[readIdx], ":", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("svd: expected 'name: description' for variable definition, got: '%s'", lines[readIdx])
			}
			varName, varDesc := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
			if varName == "" || varDesc == "" {
				return nil, fmt.Errorf("svd: malformed variable definition (empty name or desc): '%s'", lines[readIdx])
			}
			data.StoryVariableDefs[varName] = varDesc
			readIdx++
		}
	}

	for i := 0; i < expectedChoiceCount; i++ {
		choiceBlock := models.ChoiceBlock{}
		if readIdx >= len(lines) {
			return nil, fmt.Errorf("choice %d: unexpected EOF, expected choice number line (e.g. '%d:')", i+1, i+1)
		}
		line := lines[readIdx]
		expectedChoiceNumStr := strconv.Itoa(i + 1)
		if !strings.HasPrefix(line, expectedChoiceNumStr+":") {
			return nil, fmt.Errorf("choice %d: expected prefix '%s:', got '%s'", i+1, expectedChoiceNumStr, line)
		}
		readIdx++

		if readIdx >= len(lines) {
			return nil, fmt.Errorf("choice %d: unexpected EOF, expected NPC index", i+1)
		}
		choiceBlock.Char = strings.TrimSpace(lines[readIdx])
		if choiceBlock.Char == "" {
			return nil, fmt.Errorf("choice %d: NPC index/char identifier is empty", i+1)
		}
		readIdx++

		if readIdx >= len(lines) {
			return nil, fmt.Errorf("choice %d: unexpected EOF, expected situation description", i+1)
		}
		choiceBlock.Description = lines[readIdx]
		if choiceBlock.Description == "" {
			return nil, fmt.Errorf("choice %d: situation description is empty", i+1)
		}
		readIdx++

		options := make([]models.SceneOption, 0, 2)
		for j := 0; j < 2; j++ {
			sceneOpt := models.SceneOption{}
			var cons models.Consequences
			if readIdx >= len(lines) || !strings.HasPrefix(lines[readIdx], "ch:") {
				return nil, fmt.Errorf("choice %d, option %d: expected 'ch:', got '%s'", i+1, j+1, safeGetLine(lines, readIdx))
			}
			sceneOpt.Text = strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "ch:"))
			if sceneOpt.Text == "" {
				return nil, fmt.Errorf("choice %d, option %d: ch text is empty", i+1, j+1)
			}
			readIdx++
			if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "sv:") {
				svChanges, err := parseStoryVariableChanges(strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "sv:")), varNames)
				if err != nil {
					return nil, fmt.Errorf("choice %d, option %d: parsing sv: %w", i+1, j+1, err)
				}
				cons.StoryVariables = svChanges
				readIdx++
			}
			if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "cs:") {
				csChanges, err := parseCoreStatChanges(strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "cs:")), statNames)
				if err != nil {
					return nil, fmt.Errorf("choice %d, option %d: parsing cs: %w", i+1, j+1, err)
				}
				cons.CoreStatsChange = csChanges
				readIdx++
			}
			if readIdx < len(lines) && strings.HasPrefix(lines[readIdx], "rt:") {
				cons.ResponseText = strings.TrimSpace(strings.TrimPrefix(lines[readIdx], "rt:"))
				readIdx++
			}
			sceneOpt.Consequences = cons
			options = append(options, sceneOpt)
		}
		choiceBlock.Options = options
		data.Choices = append(data.Choices, choiceBlock)
	}

	if len(data.Choices) != expectedChoiceCount {
		return nil, fmt.Errorf("expected to parse %d choices, but found %d", expectedChoiceCount, len(data.Choices))
	}

	if readIdx != len(lines) {
		return nil, fmt.Errorf("trailing content found after parsing all %d sections, at line: '%s'", expectedChoiceCount, safeGetLine(lines, readIdx))
	}
	return data, nil
}

// getNonEmptyTrimmedLines splits text into non-empty, trimmed lines.
func getNonEmptyTrimmedLines(text string) []string {
	rawLines := strings.Split(text, "\n")
	var nonEmptyLines []string
	for _, line := range rawLines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			nonEmptyLines = append(nonEmptyLines, trimmed)
		}
	}
	return nonEmptyLines
}

// safeGetLine returns a line or placeholder if out of bounds.
func safeGetLine(lines []string, idx int) string {
	if idx >= 0 && idx < len(lines) {
		return lines[idx]
	}
	return "[EOF]"
}

// parseCoreStatChanges parses 'cs:' parts into a map from statNames to int changes.
func parseCoreStatChanges(rawChanges string, statNames []string) (map[string]int, error) {
	if rawChanges == "" {
		return nil, nil
	}
	changes := make(map[string]int)
	parts := strings.Split(rawChanges, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		subParts := strings.SplitN(p, ":", 2)
		if len(subParts) != 2 {
			return nil, fmt.Errorf("malformed core stat change part: '%s'", p)
		}
		indexStr := strings.TrimSpace(subParts[0])
		valueStr := strings.TrimSpace(subParts[1])
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			return nil, fmt.Errorf("invalid index '%s' in core stat change: '%s'", indexStr, p)
		}
		if index < 0 || index >= len(statNames) {
			return nil, fmt.Errorf("index %d out of bounds for statNames (len %d) in '%s'", index, len(statNames), p)
		}
		statName := statNames[index]
		intValue, err := strconv.Atoi(valueStr)
		if err != nil {
			return nil, fmt.Errorf("invalid integer value '%s' for stat '%s' in '%s': %w", valueStr, statName, p, err)
		}
		changes[statName] = intValue
	}
	return changes, nil
}

// parseStoryVariableChanges parses 'sv:' parts into a map from varNames to interface{} changes.
func parseStoryVariableChanges(rawChanges string, varNames []string) (map[string]interface{}, error) {
	if rawChanges == "" {
		return nil, nil
	}
	changes := make(map[string]interface{})
	parts := strings.Split(rawChanges, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		subParts := strings.SplitN(p, ":", 2)
		if len(subParts) != 2 {
			return nil, fmt.Errorf("malformed story variable change part: '%s'", p)
		}
		indexStr := strings.TrimSpace(subParts[0])
		valueStr := strings.TrimSpace(subParts[1])
		index, err := strconv.Atoi(indexStr)
		if err != nil {
			return nil, fmt.Errorf("invalid index '%s' in story variable change: '%s'", indexStr, p)
		}
		if index < 0 || index >= len(varNames) {
			return nil, fmt.Errorf("index %d out of bounds for varNames (len %d) in '%s'", index, len(varNames), p)
		}
		varName := varNames[index]
		lowerVal := strings.ToLower(valueStr)
		switch {
		case lowerVal == "true":
			changes[varName] = true
		case lowerVal == "false":
			changes[varName] = false
		default:
			if intVal, err := strconv.Atoi(valueStr); err == nil {
				changes[varName] = intVal
			} else if floatVal, err := strconv.ParseFloat(valueStr, 64); err == nil {
				changes[varName] = floatVal
			} else {
				changes[varName] = valueStr
			}
		}
	}
	return changes, nil
}
