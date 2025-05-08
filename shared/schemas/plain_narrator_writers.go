package schemas

import (
	"fmt"
	"novel-server/shared/models"
	"sort"
	"strings"
)

func FormatNarratorPlain(cfg *models.Config) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("t: %s\n", cfg.Title))
	b.WriteString(fmt.Sprintf("sd: %s\n", cfg.ShortDescription))
	if cfg.Franchise != "" {
		b.WriteString(fmt.Sprintf("fr: %s\n", cfg.Franchise))
	}
	b.WriteString(fmt.Sprintf("gn: %s\n", cfg.Genre))
	ac := 0
	if cfg.IsAdultContent {
		ac = 1
	}
	b.WriteString(fmt.Sprintf("ac: %d\n", ac))
	b.WriteString(fmt.Sprintf("pn: %s\n", cfg.PlayerName))
	b.WriteString(fmt.Sprintf("pd: %s\n", cfg.PlayerDesc))
	b.WriteString(fmt.Sprintf("wc: %s\n", cfg.WorldContext))
	b.WriteString(fmt.Sprintf("ss: %s\n", cfg.StorySummary))
	b.WriteString(fmt.Sprintf("th: %s\n", strings.Join(cfg.PlayerPrefs.Themes, ", ")))
	b.WriteString(fmt.Sprintf("st: %s\n", cfg.PlayerPrefs.Style))
	b.WriteString(fmt.Sprintf("tn: %s\n", cfg.PlayerPrefs.Tone))
	b.WriteString(fmt.Sprintf("wl: %s\n", strings.Join(cfg.PlayerPrefs.WorldLore, ", ")))
	if cfg.PlayerPrefs.PlayerDescription != "" {
		b.WriteString(fmt.Sprintf("wd: %s\n", cfg.PlayerPrefs.PlayerDescription))
	}
	if len(cfg.PlayerPrefs.DesiredLocations) > 0 {
		b.WriteString(fmt.Sprintf("dl: %s\n", strings.Join(cfg.PlayerPrefs.DesiredLocations, ", ")))
	}
	if len(cfg.PlayerPrefs.DesiredCharacters) > 0 {
		b.WriteString(fmt.Sprintf("dc: %s\n", strings.Join(cfg.PlayerPrefs.DesiredCharacters, ", ")))
	}
	if len(cfg.CoreStats) > 0 {
		b.WriteString("cs:\n")
		statNames := make([]string, 0, len(cfg.CoreStats))
		for name := range cfg.CoreStats {
			statNames = append(statNames, name)
		}
		sort.Strings(statNames)

		for _, name := range statNames {
			statDef := cfg.CoreStats[name]
			b.WriteString(fmt.Sprintf("%s: %s\n", name, statDef.Description))
		}
	}
	return b.String()
}
