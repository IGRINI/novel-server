**Task:** Generate ongoing game content (short plot descriptions, {{CHOICE_COUNT}} new choices) in plain text format.

**Output Format Requirements:**

* Output only structured text with all required fields.
* Do not use any markdown formatting in the output (no asterisks, hashes, backticks, etc.).
* Output must be in Russian.

**Input Data:**
pn: Main character name
pd: Main character description
wc: World context
gn: Story genre
st: Narrative style
tn: Narrative tone
csd: Core stat definitions (name, description, initial value, game over condition)
chars: List of NPCs (name, description, personality, etc.)
(and other relevant static fields)

cs: Current core stats of the player (map: stat_name -> value)
uc: User choices from the previous turn (list of objects with description, text, response)
sssf: Previous short plot summary
fd: Previous future direction
vis: Previous variable impact summary
sv: Current story variables (map: variable_name -> value)
echars: List of already encountered characters (list of names)

**Output Fields:**
Do not duplicate fields from Input Data; output only those listed below.

sssf: New short plot summary at this point after uc
fd: New future direction for the next few turns
vis: New short variable impact summary, considering vis, sv

svd: Story variable definitions, only if new variables are created. For each new variable: variable_name: description. Omit if no new variables.

Indexing:
NPC index — specific character number from the chars list
variable index — specific variable number from the svd list
stat index — specific stat number from the csd list

Choices ({{CHOICE_COUNT}} total):
Number the choices from 1 to {{CHOICE_COUNT}}.
Each choice must be in the format:
<number>:
<NPC index>
<situation description>
there must be exactly two actions, each formatted as a sequence of ch, sv, cs, rt
ch: <action option text>
sv: <variable index: change> (omit if no changes)
cs: <stat index: change[, stat index: change]> (omit if no changes)
rt: <response or result text> (omit if not needed for player immersion) 