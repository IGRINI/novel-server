package schemas

import (
	"fmt"
	"strconv"
	"strings"

	"novel-server/shared/models"
)

// ParseNovelSetupPlain parses the plain text output of the novel setup prompt.
func ParseNovelSetupPlain(text string, expectedNpcCount int) (*models.NovelSetupContent, error) {
	data := &models.NovelSetupContent{
		CoreStatsDefinition: make(map[string]models.StatDefinition),
		Characters:          make([]models.CharacterDefinition, 0, expectedNpcCount),
	}

	allLines := strings.Split(text, "\n")

	mode := ""
	var currentBlockLines []string
	parsedFields := make(map[string]bool)

	// Helper to finalize a csd or chars block, used DRYly
	finalizeCurrentBlock := func() error {
		if mode == "csd" && len(currentBlockLines) > 0 {
			if len(currentBlockLines) != 4 {
				return fmt.Errorf("csd: incomplete stat block at end of section (got %d lines for stat starting with '%s', expected 4)", len(currentBlockLines), currentBlockLines[0])
			}
			statName, statDef, err := parseNovelSetupStat(currentBlockLines)
			if err != nil {
				return fmt.Errorf("csd: error parsing final stat: %w", err)
			}
			data.CoreStatsDefinition[statName] = statDef
			currentBlockLines = []string{}
		} else if mode == "chars" && len(currentBlockLines) > 0 {
			if len(currentBlockLines) != 7 {
				return fmt.Errorf("chars: incomplete character block at end of section (got %d lines for char starting with '%s', expected 7)", len(currentBlockLines), currentBlockLines[0])
			}
			charDef, err := parseNovelSetupCharacter(currentBlockLines)
			if err != nil {
				return fmt.Errorf("chars: error parsing final character: %w", err)
			}
			data.Characters = append(data.Characters, charDef)
			currentBlockLines = []string{}
		}
		return nil
	}

	for _, rawLine := range allLines {
		line := strings.TrimSpace(rawLine)

		if line == "" {
			if err := finalizeCurrentBlock(); err != nil {
				return nil, err
			}
			mode = ""
			continue
		}

		switch {
		case strings.HasPrefix(line, "spi:"):
			if err := finalizeCurrentBlock(); err != nil {
				return nil, err
			}
			mode = ""
			val := strings.TrimSpace(line[len("spi:"):])
			if val == "" {
				return nil, fmt.Errorf("spi field is empty")
			}
			data.StoryPreviewImagePrompt = val
			parsedFields["spi"] = true
		case strings.HasPrefix(line, "sssf:"):
			if err := finalizeCurrentBlock(); err != nil {
				return nil, err
			}
			mode = ""
			val := strings.TrimSpace(line[len("sssf:"):])
			if val == "" {
				return nil, fmt.Errorf("sssf field is empty")
			}
			data.StorySummarySoFar = val
			parsedFields["sssf"] = true
		case strings.HasPrefix(line, "fd:"):
			if err := finalizeCurrentBlock(); err != nil {
				return nil, err
			}
			mode = ""
			val := strings.TrimSpace(line[len("fd:"):])
			if val == "" {
				return nil, fmt.Errorf("fd field is empty")
			}
			data.FutureDirection = val
			parsedFields["fd"] = true
		case line == "csd:":
			if err := finalizeCurrentBlock(); err != nil {
				return nil, err
			}
			mode = "csd"
			currentBlockLines = []string{}
			parsedFields["csd"] = true
		case line == "chars:":
			if err := finalizeCurrentBlock(); err != nil {
				return nil, err
			}
			mode = "chars"
			currentBlockLines = []string{}
			parsedFields["chars"] = true
		default:
			if mode == "csd" {
				currentBlockLines = append(currentBlockLines, line)
				if len(currentBlockLines) == 4 {
					statName, statDef, err := parseNovelSetupStat(currentBlockLines)
					if err != nil {
						return nil, fmt.Errorf("csd: error parsing stat block: %w. Block: %v", err, currentBlockLines)
					}
					data.CoreStatsDefinition[statName] = statDef
					currentBlockLines = []string{}
				}
			} else if mode == "chars" {
				currentBlockLines = append(currentBlockLines, line)
				if len(currentBlockLines) == 7 {
					charDef, err := parseNovelSetupCharacter(currentBlockLines)
					if err != nil {
						return nil, fmt.Errorf("chars: error parsing character block: %w. Block: %v", err, currentBlockLines)
					}
					data.Characters = append(data.Characters, charDef)
					currentBlockLines = []string{}
				}
			} else {
				return nil, fmt.Errorf("unexpected line: '%s' (current mode: '%s')", line, mode)
			}
		}
	}

	if err := finalizeCurrentBlock(); err != nil {
		return nil, err
	}

	requiredTopLevel := []string{"spi", "sssf", "fd", "csd", "chars"}
	for _, k := range requiredTopLevel {
		if !parsedFields[k] {
			return nil, fmt.Errorf("missing required top-level field or section: %s", k)
		}
	}
	if len(data.CoreStatsDefinition) != 4 {
		return nil, fmt.Errorf("expected exactly 4 core stats in csd section, found %d", len(data.CoreStatsDefinition))
	}
	if len(data.Characters) != expectedNpcCount {
		return nil, fmt.Errorf("expected exactly %d characters in chars section, found %d", expectedNpcCount, len(data.Characters))
	}
	return data, nil
}

// parseNovelSetupStat parses 4 lines of text into a models.StatDefinition.
func parseNovelSetupStat(lines []string) (string, models.StatDefinition, error) {
	if len(lines) != 4 {
		return "", models.StatDefinition{}, fmt.Errorf("stat block must have 4 lines, got %d. Content: %v", len(lines), lines)
	}
	name := strings.TrimSpace(lines[0])
	desc := strings.TrimSpace(lines[1])
	initialValStr := strings.TrimSpace(lines[2])
	goCondStr := strings.TrimSpace(lines[3])

	if name == "" {
		return "", models.StatDefinition{}, fmt.Errorf("stat name is empty")
	}
	if desc == "" {
		return "", models.StatDefinition{}, fmt.Errorf("stat description ('effects and changes') is empty for '%s'", name)
	}

	initialVal, convErr := strconv.Atoi(initialValStr)
	if convErr != nil {
		return name, models.StatDefinition{}, fmt.Errorf("invalid initial value for stat '%s': '%s' (%w)", name, initialValStr, convErr)
	}
	if initialVal < 0 || initialVal > 100 {
		return name, models.StatDefinition{}, fmt.Errorf("initial value for stat '%s' (%d) must be between 0 and 100", name, initialVal)
	}

	goCondStr = strings.ToLower(goCondStr)
	var goConditions models.GameOverConditions
	switch goCondStr {
	case "min":
		goConditions.Min = true
	case "max":
		goConditions.Max = true
	case "both":
		goConditions.Min = true
		goConditions.Max = true
	default:
		return name, models.StatDefinition{}, fmt.Errorf("invalid game over condition for stat '%s': '%s' (must be min, max, or both)", name, goCondStr)
	}

	stat := models.StatDefinition{
		Description: desc,
		Initial:     initialVal,
		Go:          goConditions,
	}
	return name, stat, nil
}

// parseNovelSetupCharacter parses 7 lines of text into a models.CharacterDefinition (including image reference).
func parseNovelSetupCharacter(lines []string) (models.CharacterDefinition, error) {
	if len(lines) != 7 {
		return models.CharacterDefinition{}, fmt.Errorf("character block must have 7 lines (name, description, personality, relationships, attitude, prompt, image_ref), got %d. Content: %v", len(lines), lines)
	}
	name := lines[0]
	desc := lines[1]
	personality := lines[2]
	relationships := lines[3]
	attitude := lines[4]
	imgPrompt := lines[5]
	imageRef := lines[6]

	if name == "" {
		return models.CharacterDefinition{}, fmt.Errorf("character name is empty")
	}
	if desc == "" {
		return models.CharacterDefinition{}, fmt.Errorf("character '%s' description is empty", name)
	}
	if personality == "" {
		return models.CharacterDefinition{}, fmt.Errorf("character '%s' personality is empty", name)
	}
	if relationships == "" {
		return models.CharacterDefinition{}, fmt.Errorf("character '%s' relationships is empty", name)
	}
	if attitude == "" {
		return models.CharacterDefinition{}, fmt.Errorf("character '%s' attitude_to_player is empty", name)
	}
	if imgPrompt == "" {
		return models.CharacterDefinition{}, fmt.Errorf("character '%s' image prompt is empty", name)
	}
	if imageRef == "" {
		return models.CharacterDefinition{}, fmt.Errorf("character '%s' image reference is empty", name)
	}

	return models.CharacterDefinition{
		Name:             name,
		Description:      desc,
		Personality:      personality,
		Relationships:    relationships,
		AttitudeToPlayer: attitude,
		Prompt:           imgPrompt,
		ImageRef:         imageRef,
	}, nil
}
