**Task:** Generate initial game state components based on the provided input configuration.

**Output Format Requirements:**

Output only structured text with all required fields.
Do not use any markdown formatting (no asterisks, hashes, backticks, etc.).
This is the complete setup; the player will use it directly for game generation.
ALL IN RUSSIAN
All image prompts (pr and spi) must follow a unified style: moody, high-contrast digital illustration with dark tones, soft neon accents, focused central composition blending fantasy and minimalism, deep blues, teals, cyan glow, occasional purples for atmosphere.

**Input Data:**
The AI receives a configuration block with the following fields:

t: story title
sd: very short plot overview
fr: franchise (if applicable)
gn: story genre
ac: adult content flag, determined by AI
pn: main character name
pd: main character description
wc: world context
ss: short summary of the story arc
th: themes, a list of keywords
st: narrative style
tn: tone of storytelling
wl: current world lore
wd: optional extra player details
dl: optional desired locations
dc: optional desired characters
cs: core stats definitions object
pp: player preferences

**Output Fields:**

spi: very short story preview image prompt (in English), based on wc, ss, gn, fr, and th
sssf: story summary so far describes the very beginning of the story
fd: future direction outlines a plan for the first scene
csd: core stats definitions matching input cs; for each stat:
stat name
(new line) must specify what this stat affects, what it depends on, and what causes it to change
(new line) initial value (must be an integer number between 0 and 100, only digits)
(new line) exactly one of "min", "max", or "both" (no numeric value), indicating the axis or axes that end the story
chars: exactly {{NPC_COUNT}} NPCs, each:
name
(new line) description (no prefix, just the text; a short summary of the character's background, skills, and role in the story)
(new line) personality (no prefix, just the text; a brief description of the character's temperament, behavior, and typical reactions)
(new line) relationships (no prefix, just the text; how this character relates to other NPCs, including alliances, rivalries, or special bonds)
(new line) attitude_to_player (no prefix, just the text; how this character feels about and interacts with the main character)
(new line) very short image prompt in English (no prefix, just the text; a concise visual description for generating the character's portrait in the required style. If the character is well-known (e.g., Harry Potter), the prompt should primarily be their name.)
(new line) image reference (ir) (no prefix, just the text; deterministic identifier for the image filename/link. If the character is well-known (e.g., Harry Potter, Doomguy), use their name in snake_case (e.g., `harry_potter`, `doomguy`). Otherwise, use a format like `[gender]_[age_category]_[theme]_[desc_word1]_[desc_word2]`, where `[age_category]` is one of 'child', 'young', 'adult', 'old'. NPCs should have identical `ir` for visual consistency if they represent the same visual type, even if minor description details vary.)
