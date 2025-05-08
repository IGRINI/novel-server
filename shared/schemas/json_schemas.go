package schemas

import (
	"context"
	"errors"
	"fmt"
	"log"
	"novel-server/shared/interfaces"
	"novel-server/shared/models"
	"strconv"
)

// --- Helper function to get dynamic config value ---
func getDynamicInt(ctx context.Context, dynConfRepo interfaces.DynamicConfigRepository, db interfaces.DBTX, configKey string, defaultValue int) int {
	value := defaultValue
	dynConf, err := dynConfRepo.GetByKey(ctx, db, configKey)
	if err != nil {
		if !errors.Is(err, models.ErrNotFound) {
			log.Printf("[WARN] Failed to get dynamic config for %s, using default %d: %v", configKey, defaultValue, err)
		} else {
			// Log less verbosely if not found
			log.Printf("[DEBUG] Dynamic config for %s not found, using default %d", configKey, defaultValue)
		}
	} else if dynConf != nil && dynConf.Value != "" {
		if parsedValue, convErr := strconv.Atoi(dynConf.Value); convErr == nil && parsedValue > 0 {
			value = parsedValue
			log.Printf("[DEBUG] Using dynamic config for %s: %d", configKey, value)
		} else {
			log.Printf("[WARN] Failed to parse dynamic config value for %s ('%s') as positive integer, using default %d: %v",
				configKey, dynConf.Value, defaultValue, convErr)
		}
	}
	return value
}

// GetOpenAIJSONSchemaObject возвращает JSON схему в виде map[string]interface{}
// и предлагаемое имя для схемы, подходящее для использования с OpenAI API response_format.json_schema.
// ТЕПЕРЬ ПРИНИМАЕТ ЗАВИСИМОСТИ ДЛЯ ДИНАМИЧЕСКОЙ КОНФИГУРАЦИИ.
func GetOpenAIJSONSchemaObject(
	ctx context.Context,
	dynConfRepo interfaces.DynamicConfigRepository,
	db interfaces.DBTX,
	promptType models.PromptType,
) (schema map[string]interface{}, schemaName string, err error) {

	// Получаем динамические значения NPC_COUNT и CHOICE_COUNT
	npcCount := getDynamicInt(ctx, dynConfRepo, db, "generation.npc_count", 3)       // Default 3 NPCs
	choiceCount := getDynamicInt(ctx, dynConfRepo, db, "generation.choice_count", 2) // Default 2 Choice blocks

	switch promptType {
	case models.PromptTypeNarrator:
		schemaName = "generate_narrator_config"
		schema = map[string]interface{}{
			"type":                 "object",
			"description":          "Schema for generating a new game configuration.",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"t":      map[string]interface{}{"type": "string", "description": "Title of the game/story."},
				"sd":     map[string]interface{}{"type": "string", "description": "Short description of the game/story."},
				"fr":     map[string]interface{}{"type": "string", "description": "Franchise, if applicable (e.g., Star Wars, Lord of the Rings)."},
				"gn":     map[string]interface{}{"type": "string", "description": "Genre of the game (e.g., Fantasy, Sci-Fi, Horror)."},
				"ac":     map[string]interface{}{"type": "boolean", "description": "Indicates if the content is adult-oriented. Should be auto-determined by the AI."},
				"pn":     map[string]interface{}{"type": "string", "description": "Player's character name. Should be specific unless a generic name is requested."},
				"pg":     map[string]interface{}{"type": "string", "description": "Player's character gender."},
				"p_desc": map[string]interface{}{"type": "string", "description": "Description of the player's character."},
				"wc":     map[string]interface{}{"type": "string", "description": "World context and background lore."},
				"ss":     map[string]interface{}{"type": "string", "description": "Overall story summary or premise."},
				"sssf":   map[string]interface{}{"type": "string", "description": "Story summary so far, describing the very start of the story."},
				"fd":     map[string]interface{}{"type": "string", "description": "Future direction, outlining the plan for the first scene or initial story arc."},
				"cs": map[string]interface{}{
					"type":        "object",
					"description": "Core statistics for the game. MUST contain EXACTLY 4 unique stats. Stat names and descriptions should be in the System Prompt language.",
					"properties":  map[string]interface{}{
						// Properties are dynamically generated stat names.
						// Example structure for one stat:
						// "stat_name_1": {"d": "desc_1", "iv": 50, "go": {"min": true, "max": true}}
					},
					"additionalProperties": map[string]interface{}{ // Defines the structure for each stat under "cs"
						"type": "object",
						"properties": map[string]interface{}{
							"d":  map[string]interface{}{"type": "string", "description": "Description of the stat."},
							"iv": map[string]interface{}{"type": "integer", "description": "Initial value of the stat (0-100)."},
							"go": map[string]interface{}{
								"type":        "object",
								"description": "Game Over conditions related to this stat.",
								"properties": map[string]interface{}{
									"min": map[string]interface{}{"type": "boolean", "description": "If true, game over if stat reaches minimum (usually 0)."},
									"max": map[string]interface{}{"type": "boolean", "description": "If true, game over if stat reaches maximum (usually 100)."},
								},
								"required":             []string{"min", "max"},
								"additionalProperties": false, // Это для объекта "go"
							},
						},
						"required":             []string{"d", "iv", "go"},
						"additionalProperties": false, // <--- ДОБАВЛЕНО для схемы индивидуального стата
					},
				},
				"pp": map[string]interface{}{
					"type":        "object",
					"description": "Player preferences for the game.",
					"properties": map[string]interface{}{
						"th":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Themes for the story."},
						"st":     map[string]interface{}{"type": "string", "description": "Visual and narrative style (MUST be English)."},
						"tn":     map[string]interface{}{"type": "string", "description": "Tone of the story (e.g., serious, comedic)."},
						"p_desc": map[string]interface{}{"type": "string", "description": "Optional extra player details or background."},
						"wl":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Specific world lore elements to include."},
						"dl":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional desired locations to feature."},
						"dc":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Optional desired characters or types of characters."},
						"cvs":    map[string]interface{}{"type": "string", "description": "Character visual style - a detailed visual prompt for generating character images (MUST be English)."},
					},
					"required":             []string{"th", "st", "tn", "p_desc", "wl", "dl", "dc", "cvs"},
					"additionalProperties": false,
				},
			},
			"required": []string{"t", "sd", "fr", "gn", "ac", "pn", "pg", "p_desc", "wc", "ss", "sssf", "fd", "cs", "pp"},
		}
		return schema, schemaName, nil

	case models.PromptTypeNarratorReviser:
		schemaName = "revise_narrator_config"
		// Schema is identical to PromptTypeNarrator as it revises the same structure.
		// The "ur" key is part of the input to the AI, not the output schema.
		narratorSchema, _, err := GetOpenAIJSONSchemaObject(ctx, dynConfRepo, db, models.PromptTypeNarrator)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get base narrator schema for reviser: %w", err)
		}
		if narratorSchema == nil {
			return nil, "", fmt.Errorf("base narrator schema for reviser is nil")
		}
		// Клонируем мапу, чтобы не изменять оригинальную кешированную схему
		revisedSchema := make(map[string]interface{})
		for k, v := range narratorSchema {
			revisedSchema[k] = v
		}
		revisedSchema["description"] = "Schema for revising an existing game configuration based on user instructions."
		return revisedSchema, schemaName, nil

	case models.PromptTypeNovelSetup:
		schemaName = "generate_novel_setup"
		schema = map[string]interface{}{
			"type":                 "object",
			"description":          "Schema for generating initial game setup (core stat definitions, characters, story preview image prompt).",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"csd": map[string]interface{}{
					"type":        "object",
					"description": "Core Stats Definition. Use exact stat names & game over conditions from input config's 'cs' field. Enhance descriptions. Add icon names.",
					"additionalProperties": map[string]interface{}{ // Defines structure for each stat under "csd"
						"type": "object",
						"properties": map[string]interface{}{
							"iv": map[string]interface{}{"type": "integer", "description": "Initial value (0-100).", "minimum": 0, "maximum": 100},
							"d":  map[string]interface{}{"type": "string", "description": "Enhanced description of the stat (in System Prompt language)."},
							"go": map[string]interface{}{
								"type":                 "object",
								"description":          "Game Over conditions (mirrored from input config's 'cs'.stat.go).",
								"additionalProperties": false, // <--- ДОБАВЛЕНО для GO внутри CSD стата
							},
							"ic": map[string]interface{}{
								"type":        "string",
								"description": "Icon name for the stat.",
								"enum": []string{ // From novel_setup.md
									"Crown", "Flag", "Ring", "Throne", "Person", "GroupOfPeople", "TwoHands", "Mask", "Compass",
									"Pyramid", "Dollar", "Lightning", "Sword", "Shield", "Helmet", "Spear", "Axe", "Bow",
									"Star", "Gear", "WarningTriangle", "Mountain", "Eye", "Skull", "Fire", "Pentagram",
									"Book", "Leaf", "Cane", "Scales", "Heart", "Sun",
								},
							},
						},
						"required":             []string{"iv", "d", "go", "ic"},
						"additionalProperties": false, // <--- ДОБАВЛЕНО для схемы индивидуального стата в CSD
					},
				},
				"chars": map[string]interface{}{
					"type":        "array",
					"description": fmt.Sprintf("Array of exactly %d Non-Player Characters (NPCs). Player character is NOT included here. Names, descriptions, personalities in System Prompt lang. Visual tags, prompts, refs in English.", npcCount),
					"minItems":    npcCount,
					"maxItems":    npcCount,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"n":  map[string]interface{}{"type": "string", "description": "NPC's name."},
							"d":  map[string]interface{}{"type": "string", "description": "NPC's detailed description."},
							"vt": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "Visual tags describing the NPC (English)."},
							"p":  map[string]interface{}{"type": "string", "description": "NPC's personality traits."},
							"pr": map[string]interface{}{"type": "string", "description": "Detailed image prompt for generating NPC's portrait (English)."},
							"ir": map[string]interface{}{"type": "string", "description": "Image reference string, can be deterministic (e.g., snake_case_name or [gender]_[age]_[theme]) (English)."},
						},
						"required":             []string{"n", "d", "vt", "p", "pr", "ir"},
						"additionalProperties": false, // <--- ДОБАВЛЕНО для схемы индивидуального NPC
					},
				},
				"spi": map[string]interface{}{"type": "string", "description": "Story Preview Image prompt (detailed, in English, based on world context, story summary, genre, franchise, themes)."},
			},
			"required": []string{"csd", "chars", "spi"},
		}
		return schema, schemaName, nil

	case models.PromptTypeNovelFirstSceneCreator:
		schemaName = "generate_novel_first_scene"
		schema = map[string]interface{}{
			"type":                 "object",
			"description":          "Schema for generating the first scene of a new game, including initial choices.",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"sssf": map[string]interface{}{
					"type":        "string",
					"description": "Story Summary So Far: Full description of the story up to this current moment, establishing the initial situation. This text will be shown to the player.",
				},
				"fd": map[string]interface{}{
					"type":        "string",
					"description": "Future Direction: A brief internal note for the LLM about one or two subsequent plot arcs or possible story developments. Not shown to the player.",
				},
				"svd": map[string]interface{}{
					"type":        "object",
					"description": "Optional. Scene Variable Definitions: Defines NEW story variables introduced in this scene's choices. Keys are variable names, values are their descriptions. Omit if no new variables are introduced.",
					"additionalProperties": map[string]interface{}{
						"type":        "string",
						"description": "Description of the new variable.",
					},
				},
				"ch": map[string]interface{}{
					"type":        "array",
					"description": fmt.Sprintf("Array of exactly %d choice blocks for the player. Each block corresponds to one character or the narrator.", choiceCount),
					"minItems":    choiceCount,
					"maxItems":    choiceCount,
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"char": map[string]interface{}{
								"type":        "string",
								"description": "Name of the NPC (from NovelSetup `stp.chars[].n`) offering the choices, or 'narrator' if choices are from the storyteller. PC is NOT this character.",
							},
							"desc": map[string]interface{}{
								"type":        "string",
								"description": "Description of the current situation or context for this choice block, from the PC's perspective, involving the 'char' NPC. Markdown (*italic*, **bold**) OK.",
							},
							"opts": map[string]interface{}{
								"type":        "array",
								"description": "Array of options for the player. Exactly 2 options.",
								"minItems":    2,
								"maxItems":    2,
								"items": map[string]interface{}{
									"type": "object",
									"properties": map[string]interface{}{
										"txt": map[string]interface{}{
											"type":        "string",
											"description": "Text of the choice option shown to the player. Markdown (*italic*, **bold**) OK.",
										},
										"cons": map[string]interface{}{
											"type":        "object",
											"description": "Consequences of choosing this option. Omit sub-keys (cs, sv, gf) if they are empty/no changes.",
											"properties": map[string]interface{}{
												"cs": map[string]interface{}{
													"type":                 "object",
													"description":          "Core Stat changes (deltas, e.g., {\"Courage\": 10}). Most choices should affect stats.",
													"additionalProperties": map[string]interface{}{"type": "integer"},
												},
												"sv": map[string]interface{}{
													"type":                 "object",
													"description":          "Story Variable changes (e.g., {\"item_acquired\": \"key\"}). NEW vars must be defined in parent `svd`.",
													"additionalProperties": true, // Values can be string, bool, number
												},
												"gf": map[string]interface{}{
													"type":        "array",
													"items":       map[string]interface{}{"type": "string"},
													"description": "Global Flags set (e.g., [\"secret_found\"]).",
												},
												"rt": map[string]interface{}{
													"type":        "string",
													"description": "Optional Response Text. Adds flavor, dialogue, or info if outcome isn't obvious from txt+cs. Markdown (*italic*, **bold**) OK. Critical for questions, requests, or actions with non-obvious outcomes.",
												},
											},
											"additionalProperties": false, // <--- ДОБАВЛЕНО
										},
									},
									"required":             []string{"txt", "cons"},
									"additionalProperties": false, // <--- ДОБАВЛЕНО для объекта cons
								},
							},
						},
						"required":             []string{"char", "desc", "opts"},
						"additionalProperties": false, // <--- ДОБАВЛЕНО для объекта ch.items
					},
				},
			},
			"required": []string{"sssf", "fd", "ch"}, // svd is optional
		}
		return schema, schemaName, nil

	case models.PromptTypeNovelCreator:
		schemaName = "generate_novel_creator_scene"
		// This schema is very similar to NovelFirstSceneCreator, with the addition of 'vis'.
		baseSceneSchema, _, errSchema := GetOpenAIJSONSchemaObject(ctx, dynConfRepo, db, models.PromptTypeNovelFirstSceneCreator)
		if errSchema != nil {
			return nil, "", fmt.Errorf("failed to get base first scene schema for creator: %w", errSchema)
		}
		// Клонируем мапу, чтобы не изменять оригинальную кешированную схему
		creatorSchema := make(map[string]interface{})
		for k, v := range baseSceneSchema {
			creatorSchema[k] = v
		}

		creatorSchema["description"] = "Schema for generating ongoing gameplay content, including choices and updated world state summaries."
		// "additionalProperties": false должно уже быть в baseSceneSchema

		// Add 'vis' property
		// Убедимся, что properties является map[string]interface{}
		properties, ok := creatorSchema["properties"].(map[string]interface{})
		if !ok {
			return nil, "", fmt.Errorf("invalid 'properties' type in base schema for creator")
		}
		clonedProperties := make(map[string]interface{}) // Клонируем properties
		for k, v := range properties {
			clonedProperties[k] = v
		}
		clonedProperties["vis"] = map[string]interface{}{
			"type":        "string",
			"description": "Variable Impact Summary: A concise text summary of essential variable/flag context (from previous `vis`, `sv`, `gf`) for long-term memory.",
		}
		creatorSchema["properties"] = clonedProperties

		// Update required fields to include 'vis'
		// Убедимся, что required является []string
		required, ok := creatorSchema["required"].([]string)
		if !ok {
			return nil, "", fmt.Errorf("invalid 'required' type in base schema for creator")
		}
		clonedRequired := make([]string, len(required)) // Клонируем required
		copy(clonedRequired, required)
		creatorSchema["required"] = append(clonedRequired, "vis")

		// Обновление свойств `cs` в creatorSchema, если они существуют и наследуются
		if baseCS, ok := clonedProperties["cs"].(map[string]interface{}); ok {
			clonedCS := make(map[string]interface{}) // Клонируем cs
			for k, v := range baseCS {
				clonedCS[k] = v
			}
			delete(clonedCS, "minProperties") // Удаляем, если унаследовалось
			delete(clonedCS, "maxProperties") // Удаляем, если унаследовалось
			clonedProperties["cs"] = clonedCS
			creatorSchema["properties"] = clonedProperties // Обновляем properties в creatorSchema
		}

		// Update ch.description, ch.minItems, ch.maxItems - это уже не нужно, т.к. наследуется из PromptTypeNovelFirstSceneCreator, который уже использует choiceCount
		// chProps := properties["ch"].(map[string]interface{})  // properties теперь clonedProperties
		// chProps["description"] = fmt.Sprintf("Array of exactly %d choice blocks for the player. Each block corresponds to one character or the narrator.", choiceCount)
		// chProps["minItems"] = choiceCount
		// chProps["maxProperties"] = choiceCount

		return creatorSchema, schemaName, nil

	case models.PromptTypeNovelGameOverCreator:
		schemaName = "generate_novel_gameover_text" // Changed from _scene to _text to reflect prompt
		schema = map[string]interface{}{
			"type":        "object",
			"description": "Schema for generating a concise story ending text. The output should be ONLY `{\"et\": \"...\"}`.",
			"properties": map[string]interface{}{
				"et": map[string]interface{}{
					"type":        "string",
					"description": "The final ending text of the story (2-5 sentences). Should be concise and reflect the reason for game over, often inferred from final core stats and game context. Match game's tone/style.",
				},
				// "ch" is intentionally omitted here as per the specific instructions in novel_gameover_creator.md prompt
				// which strictly asks for `{"et": "..."}` only.
			},
			"required":             []string{"et"},
			"additionalProperties": false, // Ensures no other fields like 'ch' are included for this specific prompt
		}
		return schema, schemaName, nil

	default:
		return nil, "", fmt.Errorf("JSON schema definition not found for PromptType: %s", promptType)
	}
}
