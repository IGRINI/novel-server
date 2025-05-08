package schemas

import (
	"fmt"
	"strconv"
	"strings"

	"novel-server/shared/models"
)

// InitialCoreStat is a local temporary structure for narrator prompt output.
// It represents the initial name and description of a core stat.
type InitialCoreStat struct {
	Name        string
	Description string
}

// ParseNarratorPlain parses plain text config from narrator or narrator_reviser prompt.
// It returns the main configuration and a slice of initially defined core stats (name and description only).
func ParseNarratorPlain(text string) (*models.Config, []InitialCoreStat, error) {
	config := &models.Config{}
	var initialCoreStats []InitialCoreStat
	parsedFields := make(map[string]bool)
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) {
		line := strings.TrimSpace(lines[i])
		i++

		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "cs:") {
			parsedFields["cs"] = true
			// Expect 4 subsequent non-empty lines for stats after the "cs:" line itself.
			// The outer loop's i has already advanced past the "cs:" line.
			for statIdx := 0; statIdx < 4; statIdx++ {
				if i >= len(lines) { // Check if there are enough lines left in the input
					return nil, nil, fmt.Errorf("cs: unexpected EOF, expected line for stat %d of 4", statIdx+1)
				}
				csLine := strings.TrimSpace(lines[i])
				i++ // Consume the current line being processed for a stat

				if csLine == "" {
					// Prompt implies no blank lines instead of a stat line.
					return nil, nil, fmt.Errorf("cs: stat line %d is empty, expected 'Name:Description'", statIdx+1)
				}

				parts := strings.SplitN(csLine, ":", 2)
				if len(parts) != 2 {
					return nil, nil, fmt.Errorf("cs: stat line %d malformed (expected 'Name:Description'): '%s'", statIdx+1, csLine)
				}
				name := strings.TrimSpace(parts[0])
				desc := strings.TrimSpace(parts[1])
				if name == "" || desc == "" {
					return nil, nil, fmt.Errorf("cs: stat line %d has empty name or description: '%s'", statIdx+1, csLine)
				}
				initialCoreStats = append(initialCoreStats, InitialCoreStat{Name: name, Description: desc})
			}
			// After parsing 4 stats, the 'break' will exit the main field-parsing loop,
			// as 'cs' is the last field.
			break
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return nil, nil, fmt.Errorf("malformed line (expected key:value): '%s'", line)
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		if val == "" && key != "fr" && key != "wd" && key != "dl" && key != "dc" {
			return nil, nil, fmt.Errorf("field '%s' has an empty value but is required to have content", key)
		}
		parsedFields[key] = true

		switch key {
		case "t":
			config.Title = val
		case "sd":
			config.ShortDescription = val
		case "fr":
			config.Franchise = val
		case "gn":
			config.Genre = val
		case "ac":
			acVal, err := strconv.Atoi(val)
			if err != nil || (acVal != 0 && acVal != 1) {
				return nil, nil, fmt.Errorf("invalid value for ac (must be 0 or 1): '%s'", val)
			}
			config.IsAdultContent = (acVal == 1)
		case "pn":
			config.PlayerName = val
		case "pd":
			config.PlayerDesc = val
		case "wc":
			config.WorldContext = val
		case "ss":
			config.StorySummary = val
		case "th":
			config.PlayerPrefs.Themes = parseStringToStringSlice(val)
		case "st":
			config.PlayerPrefs.Style = val
		case "tn":
			config.PlayerPrefs.Tone = val
		case "wl":
			config.PlayerPrefs.WorldLore = parseStringToStringSlice(val)
		case "wd":
			config.PlayerPrefs.PlayerDescription = val
		case "dl":
			config.PlayerPrefs.DesiredLocations = parseStringToStringSlice(val)
		case "dc":
			config.PlayerPrefs.DesiredCharacters = parseStringToStringSlice(val)
		default:
			return nil, nil, fmt.Errorf("unknown key: '%s'", key)
		}
	}

	requiredKeys := []string{"t", "sd", "gn", "ac", "pn", "pd", "wc", "ss", "th", "st", "tn", "wl", "cs"}
	for _, k := range requiredKeys {
		if !parsedFields[k] {
			return nil, nil, fmt.Errorf("missing required field/section: %s", k)
		}
	}

	if len(initialCoreStats) != 4 {
		return nil, nil, fmt.Errorf("expected exactly 4 core stats, but found %d", len(initialCoreStats))
	}
	return config, initialCoreStats, nil
}

// parseStringToStringSlice splits a comma-separated string into a slice of trimmed strings.
func parseStringToStringSlice(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
