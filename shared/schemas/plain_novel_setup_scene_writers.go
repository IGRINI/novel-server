package schemas

import (
	"fmt"
	"novel-server/shared/models"
	"sort"
	"strings"
)

// FormatNovelSetupForScene генерирует plain-текст setup без image prompts для первой сцены
func FormatNovelSetupForScene(cfg *models.Config, data *models.NovelSetupContent) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("pn: %s\n", cfg.PlayerName))
	b.WriteString(fmt.Sprintf("pd: %s\n", cfg.PlayerDesc))
	b.WriteString(fmt.Sprintf("wc: %s\n", cfg.WorldContext))
	b.WriteString(fmt.Sprintf("gn: %s\n", cfg.Genre))
	b.WriteString(fmt.Sprintf("st: %s\n", cfg.PlayerPrefs.Style))
	b.WriteString(fmt.Sprintf("tn: %s\n", cfg.PlayerPrefs.Tone))

	b.WriteString(fmt.Sprintf("sssf: %s\n", data.StorySummarySoFar))
	b.WriteString(fmt.Sprintf("fd: %s\n", data.FutureDirection))
	b.WriteString("csd:\n")
	keys := make([]string, 0, len(data.CoreStatsDefinition))
	for name := range data.CoreStatsDefinition {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		def := data.CoreStatsDefinition[name]
		b.WriteString(fmt.Sprintf("%s\n%s\n%d\n", name, def.Description, def.Initial))
		cond := "min"
		if def.Go.Min && def.Go.Max {
			cond = "both"
		} else if def.Go.Max {
			cond = "max"
		}
		b.WriteString(fmt.Sprintf("%s\n", cond))
	}
	b.WriteString("chars:\n")
	for _, char := range data.Characters {
		b.WriteString(fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n", char.Name, char.Description, char.Personality, char.Relationships, char.AttitudeToPlayer))
	}
	return b.String()
}
