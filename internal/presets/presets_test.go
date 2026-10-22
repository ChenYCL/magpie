package presets

import (
	"encoding/json"
	"strings"
	"testing"
)

// A body a chat agent sends, with a system prompt holding a section and an
// instruction file, and two tools.
const chatBody = `{"model":"m","messages":[{"role":"system","content":"You are a coding agent.\n<skills>\n- commit\n</skills>\nContents of /w/CLAUDE.md (project instructions, checked into the codebase):\n# rules\nbe careful\n"},{"role":"user","content":"hi"}],"tools":[{"type":"function","function":{"name":"Read","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string","description":"the file"}}}}},{"type":"function","function":{"name":"Bash","description":"Run a command","parameters":{"type":"object","properties":{"command":{"type":"string"}}}}}]}`

func TestAppendPromptsAndIsCounted(t *testing.T) {
	p := Preset{ID: "t", Patches: []Patch{{ID: "a", Kind: KindPrompt, Action: ActAppend, Value: "<x>hi</x>"}}}
	out, st := Apply(p, "chat", []byte(chatBody))
	if st.Prompt.Applied != 1 || st.Applied != 1 || st.Skipped != 0 {
		t.Fatalf("stats %+v", st)
	}
	if !strings.Contains(string(out), "<x>hi</x>") || !strings.HasSuffix(strings.TrimSpace(string(out)), "}") {
		t.Fatalf("append didn't land: %s", out)
	}
	// the model and the stream flag are never the preset's to change
	var before, after map[string]any
	json.Unmarshal([]byte(chatBody), &before)
	json.Unmarshal(out, &after)
	if before["model"] != after["model"] {
		t.Fatal("the preset changed the model")
	}
}

func TestNoPatchLeavesTheBytesAsTheyAre(t *testing.T) {
	out, st := Apply(Preset{ID: "empty"}, "chat", []byte(chatBody))
	if string(out) != chatBody {
		t.Fatal("an empty preset changed the body")
	}
	if st.Applied != 0 || st.Total != 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestDropSection(t *testing.T) {
	p := Preset{ID: "t", Patches: []Patch{{ID: "d", Kind: KindPrompt, Action: ActDrop, Match: "skills"}}}
	out, st := Apply(p, "chat", []byte(chatBody))
	if st.Prompt.Applied != 1 {
		t.Fatalf("stats %+v", st)
	}
	body := string(out)
	if strings.Contains(body, "<skills>") || !strings.Contains(body, "Contents of /w/CLAUDE.md") {
		t.Fatalf("drop took the wrong thing: %s", body)
	}
}

func TestSetSection(t *testing.T) {
	p := Preset{ID: "t", Patches: []Patch{{ID: "s", Kind: KindPrompt, Action: ActSet, Match: "skills", Value: "- none"}}}
	out, _ := Apply(p, "chat", []byte(chatBody))
	if !strings.Contains(string(out), "<skills>- none</skills>") {
		t.Fatalf("set didn't land: %s", out)
	}
}

func TestMissingSectionIsSkippedAndSaid(t *testing.T) {
	p := Preset{ID: "t", Patches: []Patch{{ID: "d", Kind: KindPrompt, Action: ActDrop, Match: "nope"}}}
	_, st := Apply(p, "chat", []byte(chatBody))
	if st.Prompt.Skipped != 1 || st.Applied != 0 {
		t.Fatalf("stats %+v", st)
	}
	if len(st.Notes) != 1 || !strings.Contains(st.Notes[0], "nope") {
		t.Fatalf("notes %v", st.Notes)
	}
}

func TestOffPatchCountsAsSkipped(t *testing.T) {
	p := Preset{ID: "t", Patches: []Patch{{ID: "d", Kind: KindPrompt, Action: ActDrop, Match: "skills", Disabled: true}}}
	out, st := Apply(p, "chat", []byte(chatBody))
	if string(out) != chatBody {
		t.Fatal("an off patch changed the body")
	}
	if st.Prompt.Total != 1 || st.Prompt.Skipped != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestContextCompactKeepsThePath(t *testing.T) {
	p := Preset{ID: "t", Patches: []Patch{{ID: "c", Kind: KindContext, Action: ActCompactContext, Match: "CLAUDE.md"}}}
	out, st := Apply(p, "chat", []byte(chatBody))
	if st.Context.Applied != 1 {
		t.Fatalf("stats %+v", st)
	}
	body := string(out)
	if !strings.Contains(body, "Contents of /w/CLAUDE.md") {
		t.Fatalf("the file's path went with its text: %s", body)
	}
	if strings.Contains(body, "be careful") {
		t.Fatalf("the file's text stayed: %s", body)
	}
}

func TestContextDenyAllRemovesEveryFile(t *testing.T) {
	p := Preset{ID: "t", Patches: []Patch{{ID: "c", Kind: KindContext, Action: ActDeny, Match: "*"}}}
	out, st := Apply(p, "chat", []byte(chatBody))
	if st.Context.Applied != 1 {
		t.Fatalf("stats %+v", st)
	}
	if strings.Contains(string(out), "be careful") {
		t.Fatal("deny * left the file")
	}
}

func TestToolsDenyAndAllow(t *testing.T) {
	deny := Preset{ID: "t", Patches: []Patch{{ID: "x", Kind: KindTools, Action: ActDeny, Match: "Bash"}}}
	out, st := Apply(deny, "chat", []byte(chatBody))
	if st.Tools.Applied != 1 {
		t.Fatalf("stats %+v", st)
	}
	if strings.Contains(string(out), `"Bash"`) || !strings.Contains(string(out), `"Read"`) {
		t.Fatalf("deny took the wrong tool: %s", out)
	}

	allow := Preset{ID: "t", Patches: []Patch{{ID: "x", Kind: KindTools, Action: ActAllow, Match: "Read"}}}
	out, _ = Apply(allow, "chat", []byte(chatBody))
	if strings.Contains(string(out), `"Bash"`) || !strings.Contains(string(out), `"Read"`) {
		t.Fatalf("allow kept the wrong tool: %s", out)
	}
}

func TestToolsCompactDropsProseNotShape(t *testing.T) {
	p := Preset{ID: "t", Patches: []Patch{{ID: "x", Kind: KindTools, Action: ActCompactTools, Match: "Read"}}}
	out, st := Apply(p, "chat", []byte(chatBody))
	if st.Tools.Applied != 1 {
		t.Fatalf("stats %+v", st)
	}
	body := string(out)
	if strings.Contains(body, "Read a file") || strings.Contains(body, "the file") {
		t.Fatalf("compact left the prose: %s", body)
	}
	if !strings.Contains(body, `"path"`) || !strings.Contains(body, `"type":"object"`) {
		t.Fatalf("compact took the shape: %s", body)
	}
	// the tool Bash keeps its description: the patch named Read alone
	if !strings.Contains(body, "Run a command") {
		t.Fatalf("compact touched a tool it didn't name: %s", body)
	}
}

func TestAnthropicSystemAndTools(t *testing.T) {
	body := `{"model":"m","max_tokens":8,"system":[{"type":"text","text":"You are Claude Code.\n<skills>x</skills>"}],"tools":[{"name":"Read","description":"d","input_schema":{"type":"object","properties":{"p":{"type":"string"}}}}],"messages":[{"role":"user","content":"hi"}]}`
	p := Preset{ID: "t", Patches: []Patch{
		{ID: "d", Kind: KindPrompt, Action: ActDrop, Match: "skills"},
		{ID: "x", Kind: KindTools, Action: ActRename, Match: "Read", Value: "read_file"},
	}}
	out, st := Apply(p, "anthropic", []byte(body))
	if st.Applied != 2 {
		t.Fatalf("stats %+v", st)
	}
	s := string(out)
	if strings.Contains(s, "<skills>") || !strings.Contains(s, `"read_file"`) {
		t.Fatalf("anthropic patches didn't land: %s", s)
	}
	// the block shape it came in stays
	if !strings.Contains(s, `"type":"text"`) {
		t.Fatalf("the system blocks lost their shape: %s", s)
	}
}

func TestResponsesInstructionsAndGemini(t *testing.T) {
	resp := `{"model":"m","instructions":"You are Codex.","tools":[{"type":"function","name":"shell","description":"run","parameters":{"type":"object"}}],"input":"hi"}`
	out, st := Apply(Preset{ID: "t", Patches: []Patch{{ID: "a", Kind: KindPrompt, Action: ActAppend, Value: "<x>y</x>"}}}, "responses", []byte(resp))
	if st.Prompt.Applied != 1 || !strings.Contains(string(out), "You are Codex.") || !strings.Contains(string(out), "<x>y</x>") {
		t.Fatalf("responses append: %+v %s", st, out)
	}

	gem := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"systemInstruction":{"parts":[{"text":"You are Gemini."}]},"tools":[{"functionDeclarations":[{"name":"run","description":"d"}]}]}`
	out, st = Apply(Preset{ID: "t", Patches: []Patch{{ID: "a", Kind: KindPrompt, Action: ActAppend, Value: "<x>y</x>"}}}, "gemini", []byte(gem))
	if st.Prompt.Applied != 1 || !strings.Contains(string(out), "<x>y</x>") {
		t.Fatalf("gemini append: %+v %s", st, out)
	}
}

func TestBodyWithoutASystemPromptSkipsInsteadOfAdding(t *testing.T) {
	// a chat request with no system message: an append has nothing to
	// append to, and says so rather than making one up
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	_, st := Apply(Preset{ID: "t", Patches: []Patch{{ID: "a", Kind: KindPrompt, Action: ActAppend, Value: "x"}}}, "chat", []byte(body))
	if st.Prompt.Applied != 0 || st.Prompt.Skipped != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestPresetValid(t *testing.T) {
	bad := []Preset{
		{ID: ""},
		{ID: "a b"},
		{ID: "x", Patches: []Patch{{ID: "p", Kind: "nope", Action: ActAppend}}},
		{ID: "x", Patches: []Patch{{ID: "p", Kind: KindTools, Action: ActAppend}}},
		{ID: "x", Patches: []Patch{{ID: "p", Kind: KindTools, Action: ActDeny}}},
		{ID: "x", Patches: []Patch{{ID: "p", Kind: KindTools, Action: ActDeny, Match: "a"}, {ID: "p", Kind: KindTools, Action: ActAllow, Match: "b"}}},
		{ID: "x", Isolate: map[string]bool{"wat": true}},
	}
	for _, p := range bad {
		if err := p.Valid(); err == nil {
			t.Errorf("%+v was taken", p)
		}
	}
	// every built-in is valid, and every kind of patch it names exists
	for _, p := range Builtins() {
		if err := p.Valid(); err != nil {
			t.Errorf("built-in %s: %v", p.ID, err)
		}
	}
}

func TestMatchDespiteStarsAndPipes(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"CLAUDE.md", "/w/CLAUDE.md", true},
		{"*.md", "/w/AGENTS.md", true},
		{"**/*.md", "C:\\w\\AGENTS.md", true},
		{"CLAUDE.md|AGENTS.md", "/w/AGENTS.md", true},
		{"CLAUDE.md", "/w/README.md", false},
		{"mcp__*", "mcp__browser__open", true},
		{"Read|Write", "Read", true},
		{"read", "Read", false},
	}
	for _, c := range cases {
		if got := matchPath(c.pat, c.name); got != c.want {
			// tools are case-insensitive, files are not: check tools apart
			if matchTool(c.pat, c.name) != c.want {
				t.Errorf("%q vs %q: got %v want %v", c.pat, c.name, got, c.want)
			}
		}
	}
	if !matchTool("read", "Read") {
		t.Error("tool names are case-insensitive")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	forget()
	defer forget()

	// a user's preset, an edit of a built-in, and a binding
	if err := Put(Preset{ID: "mine", Name: "Mine", Patches: []Patch{{ID: "a", Kind: KindPrompt, Action: ActAppend, Value: "x"}}}); err != nil {
		t.Fatal(err)
	}
	edited := Builtins()[0]
	edited.Sub = "edited"
	if err := Put(edited); err != nil {
		t.Fatal(err)
	}
	if err := Exclude("codex", true); err != nil {
		t.Fatal(err)
	}
	if err := SetDefault("standard"); err != nil {
		t.Fatal(err)
	}

	// read back from disk in a fresh cache
	s, err := Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Custom) != 1 || s.Custom[0].ID != "mine" {
		t.Fatalf("custom %+v", s.Custom)
	}
	if s.Builtin["standard"].Sub != "edited" {
		t.Fatalf("builtin edit %+v", s.Builtin)
	}
	if s.Default != "standard" || !s.Excluded("codex") || s.Excluded("claude-code") {
		t.Fatalf("store %+v", s)
	}
	// one preset serves every agent that isn't left out
	if p, ok := For("claude-code"); !ok || p.ID != "standard" {
		t.Fatalf("For(claude-code) = %+v", p)
	}
	if _, ok := For("codex"); ok {
		t.Fatal("an excluded agent got a preset")
	}
	// Standard is what magpie does out of the box
	if err := SetDefault(""); err != nil {
		t.Fatal(err)
	}
	if p, ok := For("claude-code"); !ok || p.ID != Standard {
		t.Fatalf("the default isn't Standard: %+v", p)
	}

	// a built-in can't be removed; a user's can, and nothing points at it
	if err := Remove("standard"); err == nil {
		t.Fatal("a built-in was removed")
	}
	if err := Remove("mine"); err != nil {
		t.Fatal(err)
	}
	if _, ok := For("claude-code"); !ok {
		t.Fatal("the agent lost its preset with the one it was given")
	}

	// reset puts the shipped text back
	if err := Reset("standard"); err != nil {
		t.Fatal(err)
	}
	if p, _ := Get("standard"); p.Sub == "edited" {
		t.Fatal("reset kept the edit")
	}
}

func TestCopyOfABuiltinIsTheUsers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	forget()
	defer forget()
	p, err := Copy("redteam", "", "我的红队")
	if err != nil {
		t.Fatal(err)
	}
	if p.Builtin || p.ID != "redteam-copy" || p.Name != "我的红队" {
		t.Fatalf("%+v", p)
	}
	if err := Remove(p.ID); err != nil {
		t.Fatalf("a copy is the user's to remove: %v", err)
	}
}

func TestStaticsCountEveryPatch(t *testing.T) {
	st := Preset{ID: "x", Patches: []Patch{
		{ID: "a", Kind: KindPrompt, Action: ActAppend},
		{ID: "b", Kind: KindTools, Action: ActDeny, Disabled: true},
	}}.Statics()
	if st.Prompt.Total != 1 || st.Tools.Total != 1 || st.Applied != 0 {
		t.Fatalf("%+v", st)
	}
}

// forget drops the store's cache, as a change on disk by another process
// would notice on its next read.
func forget() { cache = nil }

func TestAnAppendSurvivesAContextPatchOfTheLastFile(t *testing.T) {
	// the smoke run that found this: the last instruction file's chunk
	// runs to the end of the prompt, so a prompt patch (append) applied
	// before it had its text taken for part of that file. The files and
	// the tools are patched first, whatever order the patches are in.
	body := `{"model":"m","messages":[{"role":"system","content":"You are a coding agent.\nContents of /w/CLAUDE.md (project instructions, checked into the codebase):\n# rules\nbe careful\n"},{"role":"user","content":"hi"}]}`
	p := Preset{ID: "t", Patches: []Patch{
		{ID: "p1", Kind: KindPrompt, Action: ActAppend, Value: "<smoke>ran</smoke>"},
		{ID: "c1", Kind: KindContext, Action: ActCompactContext, Match: "CLAUDE.md"},
	}}
	out, st := Apply(p, "chat", []byte(body))
	if st.Prompt.Applied != 1 || st.Context.Applied != 1 {
		t.Fatalf("stats %+v", st)
	}
	s := string(out)
	if !strings.Contains(s, "<smoke>ran</smoke>") {
		t.Fatalf("the appended text went with the file's chunk: %s", s)
	}
	if strings.Contains(s, "be careful") {
		t.Fatalf("the file's text stayed: %s", s)
	}
	if !strings.Contains(s, "Contents of /w/CLAUDE.md") {
		t.Fatalf("the file's path went: %s", s)
	}
}

func TestAReminderAfterAFileStays(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"system","content":"a\nContents of /w/CLAUDE.md (project instructions):\nsecret\n</system-reminder>\nkeep me\n"},{"role":"user","content":"hi"}]}`
	p := Preset{ID: "t", Patches: []Patch{{ID: "c", Kind: KindContext, Action: ActDeny, Match: "CLAUDE.md"}}}
	out, _ := Apply(p, "chat", []byte(body))
	s := string(out)
	if strings.Contains(s, "secret") {
		t.Fatalf("the file stayed: %s", s)
	}
	if !strings.Contains(s, "keep me") || !strings.Contains(s, "</system-reminder>") {
		t.Fatalf("what the agent put after the file went: %s", s)
	}
}
