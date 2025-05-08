package schemas

import (
	"bufio"
	"fmt"
	"strings"

	"novel-server/shared/models"
)

// ParseGameOverPlain parses the plain text ending into a SceneContent with EndingText.
func ParseGameOverPlain(text string) (*models.SceneContent, error) {
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "et:") {
			endingText := strings.TrimSpace(line[len("et:"):])
			if endingText == "" {
				return nil, fmt.Errorf("et: field found but has empty content")
			}
			return &models.SceneContent{EndingText: endingText}, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan error: %w", err)
	}
	return nil, fmt.Errorf("et: field not found in text")
}
